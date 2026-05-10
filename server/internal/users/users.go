// Package users serves the profile-completion write surface introduced in
// #88: POST /users/me/profile, HEAD /users/handle/{handle}, POST
// /users/me/avatar, and the middleware gates ProfileRequired and
// AvatarRequired.
//
// Per ADR 0025, public.users no longer mirrors auth.users automatically. A
// JWT-bearing user only has a public.users row once they have completed
// profile capture (handle, DOB, ToS, display_name) via POST /users/me/profile.
// Most write surfaces in the rest of the server assume that row exists; the
// ProfileRequired middleware enforces it. AvatarRequired adds a parallel
// gate for the avatar path so a partial-signup user cannot land on the map.
package users

import (
	"context"
	"encoding/json"
	"errors"
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

// Handler owns the users-package routes and middleware.
type Handler struct {
	pool       *pgxpool.Pool
	tosVersion string
}

func New(pool *pgxpool.Pool, tosVersion string) *Handler {
	return &Handler{pool: pool, tosVersion: tosVersion}
}

// handleFormat mirrors the CHECK constraint on public.users.handle. Validating
// here gives a clean 422 instead of letting Postgres return a generic 23514.
var handleFormat = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// handleConstraintName matches the unique index created in migration 0017.
const handleConstraintName = "users_handle_key"

type postProfileBody struct {
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
	DOB           string `json:"dob"` // YYYY-MM-DD
	TOSVersion    string `json:"tos_version"`
}

// PostProfile handles POST /users/me/profile. See ADR 0025 — this is the
// sole insert path for public.users.
//
//   - 201 on first successful create.
//   - 409 profile_complete if the row already exists for this JWT subject.
//     Triggered when ON CONFLICT (id) DO NOTHING leaves RowsAffected at 0.
//   - 409 handle_taken if a different user already owns this handle.
//   - 422 on any client-side validation failure (format, age, ToS version).
func (h *Handler) PostProfile(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	var in postProfileBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	in.Handle = strings.TrimSpace(in.Handle)
	in.HandleDisplay = strings.TrimSpace(in.HandleDisplay)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.TOSVersion = strings.TrimSpace(in.TOSVersion)

	if !handleFormat.MatchString(in.Handle) {
		writeError(w, http.StatusUnprocessableEntity, "handle must match ^[a-z0-9_]{3,20}$")
		return
	}
	if strings.ToLower(in.HandleDisplay) != in.Handle {
		writeError(w, http.StatusUnprocessableEntity, "handle_display must lowercase to handle")
		return
	}
	if in.DisplayName == "" {
		writeError(w, http.StatusUnprocessableEntity, "display_name required")
		return
	}
	dob, err := time.Parse("2006-01-02", in.DOB)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "dob must be YYYY-MM-DD")
		return
	}
	if !is18OrOlder(dob, time.Now()) {
		writeError(w, http.StatusUnprocessableEntity, "must be at least 18 years old")
		return
	}
	if in.TOSVersion != h.tosVersion {
		writeError(w, http.StatusUnprocessableEntity, "tos_version mismatch; fetch current via GET /legal/tos")
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		INSERT INTO public.users (
			id, display_name, handle, handle_display,
			dob, tos_accepted_at, tos_version
		)
		VALUES ($1, $2, $3, $4, $5, now(), $6)
		ON CONFLICT (id) DO NOTHING
	`, caller, in.DisplayName, in.Handle, in.HandleDisplay, dob, in.TOSVersion)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == handleConstraintName {
			writeErrorCoded(w, http.StatusConflict, "handle_taken", "handle is already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, "insert profile: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeErrorCoded(w, http.StatusConflict, "profile_complete", "profile already created; handle is set once")
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// HeadHandle handles HEAD /users/handle/{handle}. Public-shaped for live
// signup-form validation: 200 means available, 409 means taken. The body is
// always empty (HEAD must not return one).
func (h *Handler) HeadHandle(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "handle")
	if !handleFormat.MatchString(strings.ToLower(raw)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var exists bool
	err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM public.users WHERE handle = lower($1))`,
		raw,
	).Scan(&exists)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type postAvatarBody struct {
	AvatarPath string `json:"avatar_path"`
}

// PostAvatar handles POST /users/me/avatar. The client uploads the JPEG to
// Supabase Storage under {auth.uid()}/<uuid>.jpg first; this endpoint takes
// the resulting object key and writes it to public.users.avatar_path.
//
//   - 200 on success.
//   - 400 if the path doesn't begin with the caller's UUID prefix (defense in
//     depth — the storage RLS policy already enforces this).
//   - 409 profile_required if the caller has no public.users row yet.
func (h *Handler) PostAvatar(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	var in postAvatarBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	in.AvatarPath = strings.TrimSpace(in.AvatarPath)
	if in.AvatarPath == "" {
		writeError(w, http.StatusBadRequest, "avatar_path required")
		return
	}

	// First path segment must equal the caller's UUID. Mirrors the storage
	// RLS policy avatars_owner_insert from migration 0018 — clients can only
	// upload to their own folder, so anything else is either a bug or a
	// deliberate spoof attempt.
	expectedPrefix := caller + "/"
	if !strings.HasPrefix(in.AvatarPath, expectedPrefix) {
		writeError(w, http.StatusBadRequest, "avatar_path must be under "+expectedPrefix)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`UPDATE public.users SET avatar_path = $1 WHERE id = $2`,
		in.AvatarPath, caller,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update avatar: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeErrorCoded(w, http.StatusConflict, "profile_required", "complete profile before uploading avatar")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func is18OrOlder(dob, now time.Time) bool {
	cutoff := now.AddDate(-18, 0, 0)
	return !dob.After(cutoff)
}

// ----- response helpers -----

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: msg})
}

func writeErrorCoded(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: code, Message: msg})
}

// ----- middleware -----

type ctxKey struct{ name string }

var (
	avatarPathKey = ctxKey{"avatar_path"}
)

// ProfileRequired returns 409 profile_required for any authenticated request
// from a user who has not completed POST /users/me/profile yet. Stashes the
// avatar_path (nullable) on the request context so AvatarRequired downstream
// reuses the same query — the brief calls for one round-trip per request.
//
// Caller must have already passed auth.Verifier.Middleware so a user UUID is
// in the context.
func (h *Handler) ProfileRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.UserIDFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "no user in context")
			return
		}
		var avatarPath *string
		err := h.pool.QueryRow(r.Context(),
			`SELECT avatar_path FROM public.users WHERE id = $1`,
			caller,
		).Scan(&avatarPath)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeErrorCoded(w, http.StatusConflict, "profile_required", "complete signup at /users/me/profile")
				return
			}
			writeError(w, http.StatusInternalServerError, "profile gate: "+err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), avatarPathKey, avatarPath)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AvatarRequired returns 409 avatar_required when the caller's profile row
// has no avatar_path. Must be wired after ProfileRequired in the middleware
// chain — it reads the avatar_path the upstream gate cached on the request
// context. Misuse panics on the type assertion in dev rather than silently
// double-querying.
func (h *Handler) AvatarRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap := r.Context().Value(avatarPathKey).(*string)
		if ap == nil {
			writeErrorCoded(w, http.StatusConflict, "avatar_required", "upload an avatar at /users/me/avatar")
			return
		}
		next.ServeHTTP(w, r)
	})
}
