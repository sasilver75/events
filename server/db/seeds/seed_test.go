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

	publicCount := countByVisibility(ctx, t, dbURL, "public")
	fuzzedCount := countByVisibility(ctx, t, dbURL, "fuzzed")
	if publicCount < 1 {
		t.Errorf("expected at least 1 'public' curated event, got %d", publicCount)
	}
	if fuzzedCount < 2 {
		t.Errorf("expected at least 2 'fuzzed' curated events, got %d", fuzzedCount)
	}

	publicNullDisplay := countPublicWithDisplayGeom(ctx, t, dbURL)
	if publicNullDisplay != 0 {
		t.Errorf("'public' events should have NULL display_geom; %d had it set", publicNullDisplay)
	}
	fuzzedWithinRadius := countFuzzedWithDisplayWithinRadius(ctx, t, dbURL)
	if fuzzedWithinRadius != fuzzedCount {
		t.Errorf("expected all %d fuzzed events to have display_geom within fuzz_radius_m of center; %d did", fuzzedCount, fuzzedWithinRadius)
	}

	displayBefore := snapshotFuzzedDisplay(ctx, t, dbURL)

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

	displayAfter := snapshotFuzzedDisplay(ctx, t, dbURL)
	for key, before := range displayBefore {
		after, ok := displayAfter[key]
		if !ok {
			t.Errorf("set-once violated: fuzzed event %q disappeared", key)
			continue
		}
		if before != after {
			t.Errorf("set-once violated: fuzzed event %q display_geom moved (%s → %s)", key, before, after)
		}
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

func countByVisibility(ctx context.Context, t *testing.T, dbURL, visibility string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM public.events WHERE source = 'curated' AND location_visibility = $1`,
		visibility,
	).Scan(&n); err != nil {
		t.Fatalf("count by visibility: %v", err)
	}
	return n
}

func countPublicWithDisplayGeom(ctx context.Context, t *testing.T, dbURL string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM public.events
		 WHERE source = 'curated' AND location_visibility = 'public' AND display_geom IS NOT NULL`,
	).Scan(&n); err != nil {
		t.Fatalf("count public-with-display: %v", err)
	}
	return n
}

func countFuzzedWithDisplayWithinRadius(ctx context.Context, t *testing.T, dbURL string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM public.events
		 WHERE source = 'curated'
		   AND location_visibility = 'fuzzed'
		   AND display_geom IS NOT NULL
		   AND fuzz_radius_m IS NOT NULL
		   AND ST_DistanceSphere(
		       display_geom,
		       ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)
		   ) <= fuzz_radius_m`,
	).Scan(&n); err != nil {
		t.Fatalf("count fuzzed-within-radius: %v", err)
	}
	return n
}

func snapshotFuzzedDisplay(ctx context.Context, t *testing.T, dbURL string) map[string]string {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT seed_key, ST_AsText(display_geom)
		 FROM public.events
		 WHERE source = 'curated' AND location_visibility = 'fuzzed'`,
	)
	if err != nil {
		t.Fatalf("snapshot fuzzed display: %v", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, wkt string
		if err := rows.Scan(&key, &wkt); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}
		out[key] = wkt
	}
	return out
}
