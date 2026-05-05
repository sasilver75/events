package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

const SeedCount = 3

const (
	spurSeedEmail   = "spur-seed@spur.local"
	spurSeedDisplay = "Spur Seed"
)

func Run(ctx context.Context) error {
	dbURL := os.Getenv("DATABASE_URL")
	supaURL := os.Getenv("SUPABASE_URL")
	supaKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if dbURL == "" || supaURL == "" || supaKey == "" {
		return fmt.Errorf("DATABASE_URL, SUPABASE_URL, and SUPABASE_SERVICE_ROLE_KEY must be set")
	}

	seedUserID, err := ensureSpurSeedAuthUser(ctx, supaURL, supaKey)
	if err != nil {
		return fmt.Errorf("ensure Spur Seed auth user: %w", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := upsertPublicSeedUser(ctx, conn, seedUserID); err != nil {
		return fmt.Errorf("upsert public.users seed row: %w", err)
	}

	for _, e := range curatedEvents(time.Now().UTC()) {
		if err := upsertCuratedEvent(ctx, conn, seedUserID, e); err != nil {
			return fmt.Errorf("upsert event %q: %w", e.SeedKey, err)
		}
	}
	return nil
}

func upsertPublicSeedUser(ctx context.Context, conn *pgx.Conn, id string) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO public.users (id, display_name)
		VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name
	`, id, spurSeedDisplay)
	return err
}

func upsertCuratedEvent(ctx context.Context, conn *pgx.Conn, hostID string, e CuratedEvent) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO public.events (
			host_user_id, title, description, category,
			start_at, duration_minutes, cap,
			center_lat, center_lon, source, seed_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'curated', $10)
		ON CONFLICT (seed_key) DO UPDATE SET
			host_user_id     = EXCLUDED.host_user_id,
			title            = EXCLUDED.title,
			description      = EXCLUDED.description,
			category         = EXCLUDED.category,
			start_at         = EXCLUDED.start_at,
			duration_minutes = EXCLUDED.duration_minutes,
			cap              = EXCLUDED.cap,
			center_lat       = EXCLUDED.center_lat,
			center_lon       = EXCLUDED.center_lon
	`,
		hostID, e.Title, e.Description, e.Category,
		e.StartAt, e.DurationMinutes, e.Cap,
		e.CenterLat, e.CenterLon, e.SeedKey,
	)
	return err
}

// ensureSpurSeedAuthUser returns the UUID of the Spur Seed Supabase Auth user,
// creating it via the Auth Admin API if it does not yet exist. Idempotent.
//
// The fixed email `spur-seed@spur.local` is the marker — `.local` is reserved
// for local-only addresses and will never collect mail. The Spur Seed user
// has `email_confirm: true` so it is treated as fully provisioned without
// requiring an OTP.
func ensureSpurSeedAuthUser(ctx context.Context, supaURL, serviceKey string) (string, error) {
	if id, found, err := findUserByEmail(ctx, supaURL, serviceKey, spurSeedEmail); err != nil {
		return "", err
	} else if found {
		return id, nil
	}
	return createAuthUser(ctx, supaURL, serviceKey, spurSeedEmail)
}

type adminUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type adminUsersList struct {
	Users []adminUser `json:"users"`
}

func findUserByEmail(ctx context.Context, supaURL, serviceKey, email string) (string, bool, error) {
	// Admin API supports `?email=` exact-match filter; result is the
	// `users` array shape (possibly empty).
	url := fmt.Sprintf("%s/auth/v1/admin/users?email=%s", supaURL, email)
	resp, err := adminGET(ctx, url, serviceKey)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("list users: HTTP %d: %s", resp.StatusCode, body)
	}
	var list adminUsersList
	if err := json.Unmarshal(body, &list); err != nil {
		return "", false, fmt.Errorf("decode users list: %w", err)
	}
	for _, u := range list.Users {
		if u.Email == email {
			return u.ID, true, nil
		}
	}
	return "", false, nil
}

func createAuthUser(ctx context.Context, supaURL, serviceKey, email string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"email":         email,
		"email_confirm": true,
		"user_metadata": map[string]any{"role": "spur-seed"},
	})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/auth/v1/admin/users", supaURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create user: HTTP %d: %s", resp.StatusCode, respBody)
	}
	var u adminUser
	if err := json.Unmarshal(respBody, &u); err != nil {
		return "", fmt.Errorf("decode created user: %w", err)
	}
	if u.ID == "" {
		return "", fmt.Errorf("create user: empty id in response: %s", respBody)
	}
	return u.ID, nil
}

func adminGET(ctx context.Context, url, serviceKey string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	return http.DefaultClient.Do(req)
}
