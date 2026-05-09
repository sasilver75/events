// Package auth validates Supabase-issued JWTs and exposes the authenticated
// user's UUID to downstream handlers.
//
// Identity bridging — making sure every auth.users row has a matching
// public.users row — used to happen in Postgres via the trigger introduced
// in migration 0006 (ADR 0022). #88 dropped that trigger when public.users
// gained NOT NULL profile fields the user has to type in (ADR 0025);
// POST /users/me/profile is now the sole insert path. The users package's
// RequireProfile middleware gates rule-bearing endpoints on row existence.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

type ctxKey struct{}

var userIDKey ctxKey

// Verifier validates Supabase JWTs against a JWKS that refreshes periodically.
type Verifier struct {
	jwks   keyfunc.Keyfunc
	issuer string
}

// NewVerifier fetches the JWKS once at startup, then refreshes it in the
// background. Returns an error if the initial fetch fails so the server
// fails fast on misconfiguration.
func NewVerifier(ctx context.Context, supabaseURL string) (*Verifier, error) {
	jwksURL := strings.TrimRight(supabaseURL, "/") + "/auth/v1/.well-known/jwks.json"
	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS at %s: %w", jwksURL, err)
	}
	return &Verifier{
		jwks:   k,
		issuer: strings.TrimRight(supabaseURL, "/") + "/auth/v1",
	}, nil
}

// Middleware validates the bearer token on every request and stores the
// user's UUID in the request context. Requests without a valid token get a
// 401 and never reach the handler.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, ok := bearerToken(r)
		if !ok {
			writeAuthError(w, "missing bearer token")
			return
		}
		tok, err := jwt.Parse(
			tokenStr,
			v.jwks.Keyfunc,
			jwt.WithValidMethods([]string{"ES256"}),
			jwt.WithIssuer(v.issuer),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !tok.Valid {
			writeAuthError(w, "invalid token")
			return
		}
		sub, err := tok.Claims.GetSubject()
		if err != nil || sub == "" {
			writeAuthError(w, "missing subject")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFrom returns the authenticated user's UUID from the request context.
// Only meaningful inside a handler that ran behind Middleware.
func UserIDFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok && v != ""
}

// Me is the canonical "who am I" endpoint, used as the integration-test
// surface for the auth path end-to-end.
func Me(w http.ResponseWriter, r *http.Request) {
	id, ok := UserIDFrom(r.Context())
	if !ok {
		writeAuthError(w, "no user in context")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"user_id": id})
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	t := strings.TrimSpace(h[len(prefix):])
	return t, t != ""
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="spur"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
