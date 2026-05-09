// Package friends serves the friend-graph write surface (#63, ADR 0010).
// All endpoints carry rules — duplicate-prevention, transactional mirror
// maintenance on accept and unfriend, side-of-relationship authorization —
// so the writes route through Go rather than direct PostgREST per ADR 0005.
//
// Reads can run through Supabase under the RLS policies in migration
// 0013_friendships, but the v0 iOS client routes them through these GET
// handlers so both halves share one auth surface and one HTTP shape.
package friends

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type sendRequestBody struct {
	RecipientID string `json:"recipient_id"`
}

type requestRow struct {
	Requester   string    `json:"requester"`
	Recipient   string    `json:"recipient"`
	CreatedAt   time.Time `json:"created_at"`
	DisplayName string    `json:"display_name,omitempty"`
}

type friendshipRow struct {
	UserID      string    `json:"user_id"`
	FriendID    string    `json:"friend_id"`
	CreatedAt   time.Time `json:"created_at"`
	DisplayName string    `json:"display_name,omitempty"`
}

type requestsListResponse struct {
	Incoming []requestRow `json:"incoming"`
	Outgoing []requestRow `json:"outgoing"`
}

type candidateRow struct {
	UserID        string `json:"user_id"`
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
}

// SendRequest handles POST /friends/requests.
//
// Rejects with 409 if a friendship already exists (either direction is
// equivalent under the mirrored representation, but checking the caller's
// half is sufficient) or if a request already exists in either direction —
// "you already asked them" and "they already asked you, accept it" should
// surface different UI than "request sent."
func (h *Handler) SendRequest(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	var in sendRequestBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !uuidPattern.MatchString(in.RecipientID) {
		writeError(w, http.StatusBadRequest, "invalid recipient_id")
		return
	}
	if in.RecipientID == caller {
		writeError(w, http.StatusBadRequest, "cannot friend yourself")
		return
	}

	ctx := r.Context()

	var (
		alreadyFriends    bool
		callerSentRequest bool
		callerReceivedReq bool
	)
	err := h.pool.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM public.friendships         WHERE user_id = $1 AND friend_id = $2),
			EXISTS(SELECT 1 FROM public.friendship_requests WHERE requester = $1 AND recipient = $2),
			EXISTS(SELECT 1 FROM public.friendship_requests WHERE requester = $2 AND recipient = $1)
	`, caller, in.RecipientID).Scan(&alreadyFriends, &callerSentRequest, &callerReceivedReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "preflight: "+err.Error())
		return
	}
	if alreadyFriends {
		writeError(w, http.StatusConflict, "already_friends")
		return
	}
	if callerSentRequest {
		writeError(w, http.StatusConflict, "request_already_sent")
		return
	}
	if callerReceivedReq {
		writeError(w, http.StatusConflict, "request_pending_from_them")
		return
	}

	var row requestRow
	row.Requester = caller
	row.Recipient = in.RecipientID
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO public.friendship_requests (requester, recipient)
		VALUES ($1, $2)
		RETURNING created_at
	`, caller, in.RecipientID).Scan(&row.CreatedAt); err != nil {
		// Recipient may have been deleted between the preflight and the
		// insert — treat as a normal 404, not a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			writeError(w, http.StatusNotFound, "recipient not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "insert request: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, row)
}

// AcceptRequest handles POST /friends/requests/{requester_id}/accept.
// The caller is the recipient. In one transaction: delete the matching
// request and insert both mirror rows. ADR 0010 requires both halves land
// or neither does.
func (h *Handler) AcceptRequest(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	requesterID := chi.URLParam(r, "requester_id")
	if !uuidPattern.MatchString(requesterID) {
		writeError(w, http.StatusBadRequest, "invalid requester_id")
		return
	}
	if requesterID == caller {
		writeError(w, http.StatusBadRequest, "cannot accept your own request")
		return
	}

	ctx := r.Context()

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		DELETE FROM public.friendship_requests
		WHERE requester = $1 AND recipient = $2
	`, requesterID, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete request: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "request_not_found")
		return
	}

	var callerHalf friendshipRow
	callerHalf.UserID = caller
	callerHalf.FriendID = requesterID
	if err := tx.QueryRow(ctx, `
		INSERT INTO public.friendships (user_id, friend_id) VALUES ($1, $2)
		RETURNING created_at
	`, caller, requesterID).Scan(&callerHalf.CreatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "insert caller half: "+err.Error())
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO public.friendships (user_id, friend_id) VALUES ($1, $2)
	`, requesterID, caller); err != nil {
		writeError(w, http.StatusInternalServerError, "insert requester half: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, callerHalf)
}

// RejectRequest handles DELETE /friends/requests/{requester_id}.
// The caller is the recipient.
func (h *Handler) RejectRequest(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	requesterID := chi.URLParam(r, "requester_id")
	if !uuidPattern.MatchString(requesterID) {
		writeError(w, http.StatusBadRequest, "invalid requester_id")
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		DELETE FROM public.friendship_requests
		WHERE requester = $1 AND recipient = $2
	`, requesterID, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "request_not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// WithdrawRequest handles DELETE /friends/requests/sent/{recipient_id}.
// The caller is the requester.
func (h *Handler) WithdrawRequest(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	recipientID := chi.URLParam(r, "recipient_id")
	if !uuidPattern.MatchString(recipientID) {
		writeError(w, http.StatusBadRequest, "invalid recipient_id")
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		DELETE FROM public.friendship_requests
		WHERE requester = $1 AND recipient = $2
	`, caller, recipientID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "request_not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unfriend handles DELETE /friends/{friend_id}. Deletes both mirror rows
// in one transaction (ADR 0010). If exactly one half exists at delete time,
// log it as corrupted state — ADR 0010 §Consequences leaves the response
// to "log + heal, or error" and defers the exact handling. We log and
// return success: the caller's intent ("we're not friends") is honored
// either way, and the corruption is now visible in logs for triage.
func (h *Handler) Unfriend(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	friendID := chi.URLParam(r, "friend_id")
	if !uuidPattern.MatchString(friendID) {
		writeError(w, http.StatusBadRequest, "invalid friend_id")
		return
	}

	ctx := r.Context()

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tagA, err := tx.Exec(ctx, `
		DELETE FROM public.friendships WHERE user_id = $1 AND friend_id = $2
	`, caller, friendID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete caller half: "+err.Error())
		return
	}
	tagB, err := tx.Exec(ctx, `
		DELETE FROM public.friendships WHERE user_id = $1 AND friend_id = $2
	`, friendID, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete friend half: "+err.Error())
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit: "+err.Error())
		return
	}

	a, b := tagA.RowsAffected(), tagB.RowsAffected()
	if a == 0 && b == 0 {
		writeError(w, http.StatusNotFound, "not_friends")
		return
	}
	if a != b {
		log.Printf("unfriend mirror_corruption caller=%s friend=%s caller_half=%d friend_half=%d",
			caller, friendID, a, b)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFriends handles GET /friends. Returns the caller's friends list
// joined to display_name for rendering.
func (h *Handler) ListFriends(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT f.user_id, f.friend_id, f.created_at, COALESCE(u.display_name, '')
		FROM public.friendships f
		JOIN public.users u ON u.id = f.friend_id
		WHERE f.user_id = $1
		ORDER BY f.created_at DESC
	`, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select friends: "+err.Error())
		return
	}
	defer rows.Close()

	out := make([]friendshipRow, 0)
	for rows.Next() {
		var f friendshipRow
		if err := rows.Scan(&f.UserID, &f.FriendID, &f.CreatedAt, &f.DisplayName); err != nil {
			writeError(w, http.StatusInternalServerError, "scan: "+err.Error())
			return
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ListRequests handles GET /friends/requests. Returns incoming and
// outgoing in one shape so the iOS Friends tab fetches once.
func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	ctx := r.Context()

	incoming, err := h.queryRequests(ctx, `
		SELECT fr.requester, fr.recipient, fr.created_at, COALESCE(u.display_name, '')
		FROM public.friendship_requests fr
		JOIN public.users u ON u.id = fr.requester
		WHERE fr.recipient = $1
		ORDER BY fr.created_at DESC
	`, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select incoming: "+err.Error())
		return
	}
	outgoing, err := h.queryRequests(ctx, `
		SELECT fr.requester, fr.recipient, fr.created_at, COALESCE(u.display_name, '')
		FROM public.friendship_requests fr
		JOIN public.users u ON u.id = fr.recipient
		WHERE fr.requester = $1
		ORDER BY fr.created_at DESC
	`, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select outgoing: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, requestsListResponse{
		Incoming: incoming,
		Outgoing: outgoing,
	})
}

// SearchCandidates handles GET /friends/candidates?q=<handle>.
// Exact match on the lowercased handle (#88 switched from display_name to
// handle since display_name is non-unique under ADR 0025). The query is
// lowercased server-side so the iOS field can be permissive about casing.
// Excludes the caller themselves so the "send request" affordance doesn't
// appear on the user's own row. Existing-friend / pending-request
// filtering is intentionally left to the SendRequest 409 path so the
// result list is one query, not three.
func (h *Handler) SearchCandidates(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q required")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, handle, handle_display, display_name
		FROM public.users
		WHERE handle = lower($1) AND id <> $2
		LIMIT 20
	`, q, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select candidates: "+err.Error())
		return
	}
	defer rows.Close()

	out := make([]candidateRow, 0)
	for rows.Next() {
		var c candidateRow
		if err := rows.Scan(&c.UserID, &c.Handle, &c.HandleDisplay, &c.DisplayName); err != nil {
			writeError(w, http.StatusInternalServerError, "scan: "+err.Error())
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) queryRequests(ctx context.Context, sql, caller string) ([]requestRow, error) {
	rows, err := h.pool.Query(ctx, sql, caller)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]requestRow, 0)
	for rows.Next() {
		var rr requestRow
		if err := rows.Scan(&rr.Requester, &rr.Recipient, &rr.CreatedAt, &rr.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
