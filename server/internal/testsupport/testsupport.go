// Package testsupport contains test-only helpers shared across integration
// tests under server/internal/.
//
// It exists because every authenticated test needs a public.users row to
// satisfy the FKs from commits, checkins, friendships, etc. Before #88 a
// trigger on auth.users created that row automatically (ADR 0022). With the
// trigger gone (ADR 0025), tests must seed the row themselves.
package testsupport

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureProfile inserts (or updates) a public.users row with all NOT NULL
// profile columns populated. Handle is derived from displayName + userID so
// concurrent fixtures don't collide on the unique-handle constraint. Use
// EnsureProfileWithHandle when the test asserts on a specific handle.
func EnsureProfile(t *testing.T, pool *pgxpool.Pool, userID, displayName string) {
	EnsureProfileWithHandle(t, pool, userID, displayName, deriveHandle(displayName, userID))
}

// EnsureProfileWithHandle is EnsureProfile with an explicit handle. Use when
// the test queries by handle (e.g., friend search) and needs a predictable
// value.
func EnsureProfileWithHandle(t *testing.T, pool *pgxpool.Pool, userID, displayName, handle string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO public.users (
			id, display_name, handle, handle_display,
			dob, tos_accepted_at, tos_version
		)
		VALUES ($1, $2, $3, $3, '1990-01-01', now(), 'v1')
		ON CONFLICT (id) DO UPDATE SET
			display_name   = EXCLUDED.display_name,
			handle         = EXCLUDED.handle,
			handle_display = EXCLUDED.handle_display
	`, userID, displayName, handle)
	if err != nil {
		t.Fatalf("seed test profile for %s: %v", userID, err)
	}
}

func deriveHandle(displayName, userID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(displayName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		}
	}
	base := b.String()
	if len(base) > 11 {
		base = base[:11]
	}
	if base == "" {
		base = "user"
	}
	suffix := strings.ReplaceAll(userID, "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return fmt.Sprintf("%s_%s", base, suffix)
}
