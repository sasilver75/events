package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	// Internal packages — one per write surface. Each exposes a Handler
	// constructed with a *pgxpool.Pool and bound to routes below. lifecycle
	// is the in-process background loop (β-cancel today, Done-poller in #34).
	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/checkins"
	"github.com/sasilver75/events/server/internal/commits"
	"github.com/sasilver75/events/server/internal/events"
	"github.com/sasilver75/events/server/internal/friends"
	"github.com/sasilver75/events/server/internal/legal"
	"github.com/sasilver75/events/server/internal/lifecycle"
	"github.com/sasilver75/events/server/internal/users"
)

func main() {
	// --- config ---
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		log.Fatalf("SUPABASE_URL must be set")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatalf("DATABASE_URL must be set")
	}

	// --- dependencies (auth verifier + pgx pool) ---
	// appCtx scopes the lifecycle background loop. Cancelled on shutdown
	// so the loop exits cleanly before the HTTP server stops.
	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	verifier, err := auth.NewVerifier(appCtx, supabaseURL)
	if err != nil {
		log.Fatalf("auth verifier: %v", err)
	}

	pool, err := pgxpool.New(appCtx, dbURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	// --- handlers (one per internal package) ---
	eventsHandler := events.New(pool)
	commitsHandler := commits.New(pool)
	checkinsHandler := checkins.New(pool)
	friendsHandler := friends.New(pool)
	usersHandler := users.New(pool)

	// --- background lifecycle loop ---
	go lifecycle.New(pool).Run(appCtx)

	// --- router + routes ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))

	r.Get("/healthz", healthz)

	// Public surface (no JWT). The ToS is shown during signup before the
	// user has a session; the handle probe powers live availability
	// validation in the iOS sign-up form (#88, ADR 0025).
	r.Get("/legal/tos", legal.Get)
	r.Head("/users/handle/{handle}", usersHandler.HandleProbe)

	// Authenticated surface. Split into two layers:
	//   1. JWT-only: the in-flight signup endpoint that creates the
	//      public.users row. Cannot require the row to exist — that's
	//      what it's there to create.
	//   2. JWT + RequireProfile: every rule-bearing handler. Returns
	//      409 profile_required when the caller's JWT is valid but the
	//      profile row hasn't been written yet, so iOS can resume into
	//      the signup flow at the right step.
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/users/me/profile", usersHandler.UpsertProfile)

		r.Group(func(r chi.Router) {
			r.Use(users.RequireProfile(pool))
			r.Get("/me", auth.Me)
			r.Post("/users/me/avatar", usersHandler.SetAvatar)

			r.Get("/events", eventsHandler.Near)
			r.Post("/events", eventsHandler.Create)
			r.Delete("/events/{id}", eventsHandler.Cancel)
			r.Post("/events/{id}/commit", commitsHandler.Commit)
			r.Delete("/events/{id}/commit", commitsHandler.Withdraw)
			r.Post("/events/{id}/checkin", checkinsHandler.CheckIn)

			r.Get("/friends", friendsHandler.ListFriends)
			r.Delete("/friends/{friend_id}", friendsHandler.Unfriend)
			r.Get("/friends/requests", friendsHandler.ListRequests)
			r.Post("/friends/requests", friendsHandler.SendRequest)
			r.Post("/friends/requests/{requester_id}/accept", friendsHandler.AcceptRequest)
			r.Delete("/friends/requests/{requester_id}", friendsHandler.RejectRequest)
			r.Delete("/friends/requests/sent/{recipient_id}", friendsHandler.WithdrawRequest)
			r.Get("/friends/candidates", friendsHandler.SearchCandidates)
		})
	})

	// --- HTTP server lifecycle (start + graceful shutdown on SIGINT/SIGTERM) ---
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("server shutting down")
	cancelApp()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
