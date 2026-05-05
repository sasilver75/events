package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSeedIdempotent(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	supaURL := os.Getenv("SUPABASE_URL")
	supaKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if dbURL == "" || supaURL == "" || supaKey == "" {
		t.Skip("DATABASE_URL, SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY required")
	}

	ctx := context.Background()

	if err := Run(ctx); err != nil {
		t.Fatalf("first seed run: %v", err)
	}
	curatedAfterFirst := countCurated(ctx, t, dbURL)
	seedUsersAfterFirst := countSeedUsers(ctx, t, dbURL)

	if curatedAfterFirst < SeedCount {
		t.Fatalf("expected at least %d curated events after first run, got %d", SeedCount, curatedAfterFirst)
	}
	if seedUsersAfterFirst != 1 {
		t.Fatalf("expected exactly 1 Spur Seed user, got %d", seedUsersAfterFirst)
	}

	if err := Run(ctx); err != nil {
		t.Fatalf("second seed run: %v", err)
	}
	curatedAfterSecond := countCurated(ctx, t, dbURL)
	seedUsersAfterSecond := countSeedUsers(ctx, t, dbURL)

	if curatedAfterSecond != curatedAfterFirst {
		t.Errorf("non-idempotent: curated event count changed from %d to %d", curatedAfterFirst, curatedAfterSecond)
	}
	if seedUsersAfterSecond != 1 {
		t.Errorf("non-idempotent: Spur Seed user count is %d, expected 1", seedUsersAfterSecond)
	}
}

func countCurated(ctx context.Context, t *testing.T, dbURL string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.events WHERE source = 'curated'`).Scan(&n); err != nil {
		t.Fatalf("count curated events: %v", err)
	}
	return n
}

func countSeedUsers(ctx context.Context, t *testing.T, dbURL string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.users WHERE display_name = 'Spur Seed'`).Scan(&n); err != nil {
		t.Fatalf("count seed users: %v", err)
	}
	return n
}
