// Package users serves the profile-completeness write surface introduced by
// #88 (ADR 0025). POST /users/me/profile is the sole insert path for
// public.users — the row exists only after the user has supplied a handle,
// display name, DOB, ToS acceptance, and (later) an avatar path.
//
// Public endpoints:
//   - HEAD /users/handle/{handle} — uniqueness probe; 200 available, 409 taken.
//
// Authenticated endpoints:
//   - POST /users/me/profile — creates or refreshes the caller's row.
//   - POST /users/me/avatar  — sets users.avatar_path; gated by profile_required.
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/legal"
)

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// handlePattern is the canonical handle format: lowercase ASCII letters,
// digits, and underscore, length 3..20. Mirrors the CHECK constraint in
// migration 0016 — keep them in sync.
var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

type profileBody struct {
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
	DOB           string `json:"dob"` // YYYY-MM-DD
	TosVersion    string `json:"tos_version"`
}

type profileResponse struct {
	UserID        string `json:"user_id"`
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
}

// UpsertProfile handles POST /users/me/profile. Validates, then runs
// INSERT ... ON CONFLICT (id) DO UPDATE keyed on the JWT subject so signup
// retries are idempotent. The DB CHECK constraints from migration 0016
// backstop the same rules; the server-side validation here exists so the
// client gets specific 422 codes rather than a generic 23514.
func (h *Handler) UpsertProfile(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	var in profileBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	in.Handle = strings.TrimSpace(in.Handle)
	in.HandleDisplay = strings.TrimSpace(in.HandleDisplay)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.DOB = strings.TrimSpace(in.DOB)
	in.TosVersion = strings.TrimSpace(in.TosVersion)

	if !handlePattern.MatchString(in.Handle) {
		writeError(w, http.StatusUnprocessableEntity, "handle_format")
		return
	}
	if strings.ToLower(in.HandleDisplay) != in.Handle {
		writeError(w, http.StatusUnprocessableEntity, "handle_display_mismatch")
		return
	}
	if in.DisplayName == "" {
		writeError(w, http.StatusUnprocessableEntity, "display_name_empty")
		return
	}
	dob, err := time.Parse("2006-01-02", in.DOB)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "dob_format")
		return
	}
	if dob.After(eighteenYearsAgo(time.Now())) {
		writeError(w, http.StatusUnprocessableEntity, "dob_too_recent")
		return
	}
	if in.TosVersion != legal.Version {
		writeError(w, http.StatusUnprocessableEntity, "tos_version_mismatch")
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO public.users
			(id, handle, handle_display, display_name, dob, tos_accepted_at, tos_version)
		VALUES
			($1, $2, $3, $4, $5, now(), $6)
		ON CONFLICT (id) DO UPDATE SET
			handle          = EXCLUDED.handle,
			handle_display  = EXCLUDED.handle_display,
			display_name    = EXCLUDED.display_name,
			dob             = EXCLUDED.dob,
			tos_accepted_at = EXCLUDED.tos_accepted_at,
			tos_version     = EXCLUDED.tos_version
	`, caller, in.Handle, in.HandleDisplay, in.DisplayName, dob, in.TosVersion)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation — handle taken
				writeError(w, http.StatusConflict, "handle_taken")
				return
			case "23514": // check_violation — schema rule we missed in app validation
				writeError(w, http.StatusUnprocessableEntity, "constraint_violation: "+pgErr.ConstraintName)
				return
			case "23503": // foreign_key_violation — auth.users row missing
				writeError(w, http.StatusForbidden, "auth_user_missing")
				return
			}
		}
		writeError(w, http.StatusInternalServerError, "upsert profile: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, profileResponse{
		UserID:        caller,
		Handle:        in.Handle,
		HandleDisplay: in.HandleDisplay,
		DisplayName:   in.DisplayName,
	})
}

// HandleProbe handles HEAD /users/handle/{handle}. 200 if the handle is
// available, 409 if taken. Returns 422 for malformed input rather than
// silently 200ing — the client can render "invalid format" without
// committing the user to a doomed POST. Public (no JWT).
func (h *Handler) HandleProbe(w http.ResponseWriter, r *http.Request) {
	requested := chi.URLParam(r, "handle")
	normalized := strings.ToLower(requested)
	if !handlePattern.MatchString(normalized) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	var taken bool
	if err := h.pool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM public.users WHERE handle = $1)
	`, normalized).Scan(&taken); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if taken {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type avatarBody struct {
	Path string `json:"path"`
}

// SetAvatar handles POST /users/me/avatar. Accepts a Supabase Storage
// path the client just uploaded and writes it to users.avatar_path.
// The path must start with "{auth.uid()}/" so the storage RLS policy on
// the avatars bucket and this handler agree on ownership.
//
// This endpoint sits behind RequireProfile middleware (#88, ADR 0025),
// so a missing public.users row surfaces as 409 profile_required before
// reaching this handler. The defense-in-depth zero-rows-affected check
// below catches a race where the row is deleted between the middleware
// probe and this UPDATE.
func (h *Handler) SetAvatar(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	var in avatarBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	in.Path = strings.TrimSpace(in.Path)

	prefix := caller + "/"
	if !strings.HasPrefix(in.Path, prefix) || strings.Contains(in.Path[len(prefix):], "/..") {
		writeError(w, http.StatusForbidden, "avatar_path_not_owned")
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		UPDATE public.users SET avatar_path = $1 WHERE id = $2
	`, in.Path, caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update avatar: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "profile_required")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// eighteenYearsAgo returns the latest DOB acceptable for an 18+ user as
// of `now`. Mirrors the DB CHECK so the boundary stays consistent across
// the app and schema layers.
func eighteenYearsAgo(now time.Time) time.Time {
	return now.AddDate(-18, 0, 0)
}

// RequireProfile is the middleware that gates authenticated endpoints on
// public.users row existence (#88, ADR 0025). It runs one SELECT 1 per
// request after auth.Middleware. A missing row surfaces as
// 409 {"error": "profile_required"} so the iOS app can resume into the
// signup flow at the right step.
//
// The exempt endpoints — POST /users/me/profile, HEAD /users/handle/{handle},
// and GET /legal/tos — are wired in main.go outside this middleware's group.
func RequireProfile(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := auth.UserIDFrom(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "no user in context")
				return
			}
			exists, err := profileExists(r.Context(), pool, userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "profile lookup: "+err.Error())
				return
			}
			if !exists {
				writeError(w, http.StatusConflict, "profile_required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func profileExists(ctx context.Context, pool *pgxpool.Pool, userID string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.users WHERE id = $1)`, userID).Scan(&exists)
	return exists, err
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
