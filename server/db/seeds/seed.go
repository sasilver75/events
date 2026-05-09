package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

const SeedCount = 3

// Seed-user profile fields. handle is the source-of-truth lowercased
// identifier; handle_display is the cased rendering. display_name is the
// free-text label. dob is a fixed long-ago date so the 18+ CHECK passes
// regardless of the wall clock at run time.
const (
	spurSeedEmail         = "spur-seed@spur.local"
	spurSeedHandle        = "spurseed"
	spurSeedHandleDisplay = "SpurSeed"
	spurSeedDisplay       = "Spur Seed"
	spurSeedDOB           = "1990-01-01"
	spurSeedTosVersion    = "v1"
	spurSeedAvatarKey     = "spur-seed-v1.jpg"
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

	avatarPath := seedUserID + "/" + spurSeedAvatarKey
	if err := uploadSeedAvatar(ctx, supaURL, supaKey, avatarPath); err != nil {
		return fmt.Errorf("upload seed avatar: %w", err)
	}

	if err := upsertPublicSeedUser(ctx, conn, seedUserID, avatarPath); err != nil {
		return fmt.Errorf("upsert public.users seed row: %w", err)
	}

	for _, e := range curatedEvents(time.Now().UTC()) {
		if err := upsertCuratedEvent(ctx, conn, seedUserID, e); err != nil {
			return fmt.Errorf("upsert event %q: %w", e.SeedKey, err)
		}
	}
	return nil
}

func upsertPublicSeedUser(ctx context.Context, conn *pgx.Conn, id, avatarPath string) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO public.users
			(id, handle, handle_display, display_name, dob, tos_accepted_at, tos_version, avatar_path)
		VALUES
			($1, $2, $3, $4, $5::date, now(), $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			handle          = EXCLUDED.handle,
			handle_display  = EXCLUDED.handle_display,
			display_name    = EXCLUDED.display_name,
			dob             = EXCLUDED.dob,
			tos_accepted_at = EXCLUDED.tos_accepted_at,
			tos_version     = EXCLUDED.tos_version,
			avatar_path     = EXCLUDED.avatar_path
	`,
		id, spurSeedHandle, spurSeedHandleDisplay, spurSeedDisplay,
		spurSeedDOB, spurSeedTosVersion, avatarPath,
	)
	return err
}

// uploadSeedAvatar generates a tiny solid-color JPEG and uploads it to the
// avatars bucket at `{seedUserID}/{spurSeedAvatarKey}`. Idempotent — uses
// the storage upsert flag so re-running the seed doesn't 409 on the
// already-present object.
//
// The avatar is generated in-process rather than committed as a binary
// fixture so the seed runner has zero filesystem dependencies and the
// resulting image is reproducible byte-for-byte across machines.
func uploadSeedAvatar(ctx context.Context, supaURL, serviceKey, path string) error {
	jpegBytes, err := generateSeedAvatarJPEG()
	if err != nil {
		return fmt.Errorf("generate avatar bytes: %w", err)
	}

	url := fmt.Sprintf("%s/storage/v1/object/avatars/%s", supaURL, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jpegBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "image/jpeg")
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	// upsert: if the object already exists, replace it so re-runs don't 409.
	req.Header.Set("x-upsert", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("storage upload: HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

// generateSeedAvatarJPEG produces a deterministic 256x256 solid-color
// placeholder. Spur-orange (#F97316). Output stays well under the 2 MiB
// bucket cap (~2 KiB at quality 80).
func generateSeedAvatarJPEG() ([]byte, error) {
	const size = 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := color.RGBA{R: 0xF9, G: 0x73, B: 0x16, A: 0xFF}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func upsertCuratedEvent(ctx context.Context, conn *pgx.Conn, hostID string, e CuratedEvent) error {
	// COALESCE on display_geom + fuzz_radius_m enforces the set-once invariant
	// from PRD-v0:290 / CONTEXT.md §Location fuzzing — re-runs of the seed
	// must not move the fuzzed pin, otherwise repeated reads would let an
	// observer triangulate the true center.
	_, err := conn.Exec(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, seed_key,
			location_visibility, fuzz_radius_m, display_geom
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			ST_SetSRID(ST_MakePoint($8, $9), 4326),
			'curated', $10,
			$11, $12,
			CASE WHEN $13::float8 IS NULL OR $14::float8 IS NULL
			     THEN NULL
			     ELSE ST_SetSRID(ST_MakePoint($13, $14), 4326)
			END
		)
		ON CONFLICT (seed_key) DO UPDATE SET
			host_id             = EXCLUDED.host_id,
			title               = EXCLUDED.title,
			description         = EXCLUDED.description,
			category            = EXCLUDED.category,
			start_time          = EXCLUDED.start_time,
			end_time            = EXCLUDED.end_time,
			cap                 = EXCLUDED.cap,
			geom                = EXCLUDED.geom,
			location_visibility = EXCLUDED.location_visibility,
			fuzz_radius_m       = COALESCE(public.events.fuzz_radius_m, EXCLUDED.fuzz_radius_m),
			display_geom        = COALESCE(public.events.display_geom, EXCLUDED.display_geom)
	`,
		hostID, e.Title, e.Description, e.Category,
		e.StartTime, e.EndTime, e.Cap,
		e.CenterLon, e.CenterLat, e.SeedKey,
		e.LocationVisibility, e.FuzzRadiusM,
		e.DisplayLon, e.DisplayLat,
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
