// Package commits serves the Commit/Withdraw write endpoints. The Browse
// read path lives in package events; this package is intentionally narrow.
package commits

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type response struct {
	CommitCount   int  `json:"commit_count"`
	CommittedByMe bool `json:"committed_by_me"`
}

// Commit handles POST /events/{id}/commit.
//
// Concurrency: SELECT … FOR UPDATE locks the events row for the duration of
// the transaction so two simultaneous Commits at cap-1 cannot both pass the
// count check. Repeat Commits by the same user are idempotent — they short
// circuit before the cap check, so a user already inside a full event still
// receives 200.
//
// β Tip transition: when the row carries a tip_threshold and the post-Commit
// count reaches it, tipped_at is set to now() inside the same transaction.
// The transition is sticky (Withdraw never clears tipped_at) and idempotent
// (the UPDATE's WHERE matches zero rows once tipped_at is non-null). For
// α-Events the column is NULL and the UPDATE is skipped entirely.
func (h *Handler) Commit(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	eventID, ok := parseEventID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cap int
	var tipThreshold *int
	err = tx.QueryRow(ctx,
		`SELECT cap, tip_threshold FROM public.events WHERE id = $1 FOR UPDATE`, eventID,
	).Scan(&cap, &tipThreshold)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select event: "+err.Error())
		return
	}

	var alreadyCommitted bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM public.commits WHERE event_id = $1 AND user_id = $2)`,
		eventID, userID,
	).Scan(&alreadyCommitted); err != nil {
		writeError(w, http.StatusInternalServerError, "exists: "+err.Error())
		return
	}

	if !alreadyCommitted {
		var current int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM public.commits WHERE event_id = $1`, eventID,
		).Scan(&current); err != nil {
			writeError(w, http.StatusInternalServerError, "count: "+err.Error())
			return
		}
		if current >= cap {
			writeError(w, http.StatusConflict, "event_full")
			return
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
			eventID, userID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "insert: "+err.Error())
			return
		}
	}

	var finalCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM public.commits WHERE event_id = $1`, eventID,
	).Scan(&finalCount); err != nil {
		writeError(w, http.StatusInternalServerError, "final count: "+err.Error())
		return
	}

	if tipThreshold != nil && finalCount >= *tipThreshold {
		if _, err := tx.Exec(ctx,
			`UPDATE public.events
			 SET tipped_at = now()
			 WHERE id = $1 AND tipped_at IS NULL`,
			eventID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "tip update: "+err.Error())
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit tx: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response{CommitCount: finalCount, CommittedByMe: true})
}

// Withdraw handles DELETE /events/{id}/commit.
//
// Wave 1 withdraws are unconditionally clean (PRD US 23). Live-aware Flake
// gating arrives with the check-in slice (#33+).
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	eventID, ok := parseEventID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}

	ctx := r.Context()
	if _, err := h.pool.Exec(ctx,
		`DELETE FROM public.commits WHERE event_id = $1 AND user_id = $2`,
		eventID, userID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "delete: "+err.Error())
		return
	}
	var count int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM public.commits WHERE event_id = $1`, eventID,
	).Scan(&count); err != nil {
		writeError(w, http.StatusInternalServerError, "count: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response{CommitCount: count, CommittedByMe: false})
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func parseEventID(r *http.Request) (string, bool) {
	raw := chi.URLParam(r, "id")
	if !uuidPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

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
