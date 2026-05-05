package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSchemaBaseline(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; integration test requires a real Postgres")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	t.Run("postgis extension installed", func(t *testing.T) {
		var ok bool
		err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='postgis')`,
		).Scan(&ok)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if !ok {
			t.Error("postgis extension not installed")
		}
	})

	tables := []string{"users", "events", "commits"}

	t.Run("tables exist with RLS enabled", func(t *testing.T) {
		for _, table := range tables {
			var rls bool
			err := conn.QueryRow(ctx, `
				SELECT relrowsecurity
				FROM pg_class
				WHERE relname = $1
				  AND relnamespace = 'public'::regnamespace
			`, table).Scan(&rls)
			if err != nil {
				t.Errorf("public.%s missing or unreadable: %v", table, err)
				continue
			}
			if !rls {
				t.Errorf("public.%s has RLS disabled; expected enabled", table)
			}
		}
	})

	t.Run("each table has at least one select policy", func(t *testing.T) {
		for _, table := range tables {
			var n int
			err := conn.QueryRow(ctx, `
				SELECT count(*)
				FROM pg_policies
				WHERE schemaname = 'public'
				  AND tablename = $1
				  AND cmd = 'SELECT'
			`, table).Scan(&n)
			if err != nil {
				t.Errorf("policy lookup for %s: %v", table, err)
				continue
			}
			if n < 1 {
				t.Errorf("public.%s has no SELECT policy", table)
			}
		}
	})

	t.Run("events spatial index exists", func(t *testing.T) {
		var ok bool
		err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE schemaname='public'
				  AND tablename='events'
				  AND indexname='events_geog_idx'
			)
		`).Scan(&ok)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if !ok {
			t.Error("events_geog_idx not found")
		}
	})
}
