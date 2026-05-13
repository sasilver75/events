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
	"github.com/sasilver75/events/server/internal/chat"
	"github.com/sasilver75/events/server/internal/checkins"
	"github.com/sasilver75/events/server/internal/commits"
	"github.com/sasilver75/events/server/internal/events"
	"github.com/sasilver75/events/server/internal/feedback"
	"github.com/sasilver75/events/server/internal/friends"
	"github.com/sasilver75/events/server/internal/lifecycle"
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
	feedbackHandler := feedback.New(pool)
	friendsHandler := friends.New(pool)
	chatHandler := chat.New(pool)
	chatHub := chat.NewHub(pool)
	chatStream := chat.NewStream(chatHub)

	// --- background loops ---
	go lifecycle.New(pool).Run(appCtx)
	// chat hub owns the single LISTEN connection for NOTIFY chat_message
	// and fans payloads out to per-stream subscribers. Errors are logged;
	// the hub auto-reconnects with backoff inside Run.
	go func() {
		if err := chatHub.Run(appCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("chat hub: %v", err)
		}
	}()

	// --- router + routes ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", healthz)

	// SSE / streaming routes (#65): no global Timeout middleware, since
	// chat streams stay open for the lifetime of the chat sheet — minutes,
	// not seconds. The request context still cancels on client disconnect
	// (URLSession close → TCP FIN → http.Server cancels), so the handler
	// exits cleanly when the user dismisses the sheet.
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/events/{id}/messages/stream", chatStream.Handle)
	})

	// Bounded-latency routes — everything else has a 15s ceiling. Anything
	// approaching that is a bug.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(15 * time.Second))
		r.Use(verifier.Middleware)
		r.Get("/me", auth.Me)
		r.Get("/events", eventsHandler.Near)
		r.Post("/events", eventsHandler.Create)
		r.Delete("/events/{id}", eventsHandler.Cancel)
		r.Post("/events/{id}/commit", commitsHandler.Commit)
		r.Delete("/events/{id}/commit", commitsHandler.Withdraw)
		r.Post("/events/{id}/checkin", checkinsHandler.CheckIn)
		r.Get("/events/{id}/feedback", feedbackHandler.List)
		r.Post("/events/{id}/feedback", feedbackHandler.Submit)
		r.Get("/events/{id}/messages", chatHandler.History)
		r.Post("/events/{id}/messages", chatHandler.Send)

		r.Get("/friends", friendsHandler.ListFriends)
		r.Delete("/friends/{friend_id}", friendsHandler.Unfriend)
		r.Get("/friends/requests", friendsHandler.ListRequests)
		r.Post("/friends/requests", friendsHandler.SendRequest)
		r.Post("/friends/requests/{requester_id}/accept", friendsHandler.AcceptRequest)
		r.Delete("/friends/requests/{requester_id}", friendsHandler.RejectRequest)
		r.Delete("/friends/requests/sent/{recipient_id}", friendsHandler.WithdrawRequest)
		r.Get("/friends/candidates", friendsHandler.SearchCandidates)
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
