# HTTP router: chi

The Go server uses **`go-chi/chi`** (`github.com/go-chi/chi/v5`) as its HTTP router. `gin`, `echo`, and stdlib-only `net/http` are rejected.

## Why

[ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md) lines 17–18 describe the Go server's surface: a JSON-in/JSON-out HTTP server with JWT (JSON Web Token) middleware, a handful of write endpoints (Commit, Withdraw, create event, check-in, submit rating, friend-request, report) plus the geo-query browse path. That's roughly fifteen routes with shared auth middleware and per-route input validation. The router decision is about which abstraction sits between `net/http` and our handlers, not about which framework to live inside.

Three forces shape the choice.

The first is **idiomatic Go**. `chi` is built on `net/http` — handlers have signature `func(http.ResponseWriter, *http.Request)`, middleware is `func(http.Handler) http.Handler`. Anything that integrates with `net/http` (the `pgx` HTTP middleware in our stack, OpenTelemetry's `otelhttp`, third-party rate limiters) works without adapters. `gin` and `echo` define their own `*Context` type, which means handlers and middleware written against them are not portable to stdlib. For a v0 project that wants to *learn* Go's HTTP model, working in the standard interfaces is more instructive than learning a framework's bespoke ones.

The second is **the right amount of machinery**. `chi` is ~2k lines, zero reflection, gives us routing trees, URL params, route groups, sub-routers, and `With` for per-route middleware — and stops there. `gin` and `echo` ship request binding, validation helpers, response rendering, template integration, etc.; we'd either ignore those (paying for features unused) or adopt them and end up making validation/rendering decisions framework-first instead of fit-first. v0's surface is small enough that "router only" is the right scope.

The third is **escape hatch**. Because `chi` is an `http.Handler` decorator, abandoning it later is a mechanical rewrite, not a migration. Go 1.22's enhanced `net/http` mux (`GET /events/{id}` pattern matching) closes much of the gap; if `chi` ever feels like dead weight, we can rip it out without rewriting handlers. `gin`/`echo` lock-in is real.

## Shape

- **Import:** `github.com/go-chi/chi/v5` (the v5 line; v1 is a separate import path and unmaintained).
- **Mount point:** one root `chi.Router` in `cmd/server/main.go`, mounted on `http.Server`. Sub-routers per logical area (`/events`, `/users`, `/friendships`) introduced when the surface justifies it — not pre-emptively.
- **Middleware stack (initial):** `chi/middleware.RequestID`, `chi/middleware.RealIP` (Fly sets `Fly-Client-IP`), `chi/middleware.Logger`, `chi/middleware.Recoverer`, `chi/middleware.Timeout(15s)`. The JWT-validation middleware (issue #9) plugs into this stack as a `func(http.Handler) http.Handler`.
- **JSON in/out:** handlers decode/encode using `encoding/json` directly. No router-provided binding/rendering helpers — keeps validation logic explicit and grep-able.
- **URL params:** `chi.URLParam(r, "id")`. Typed parsing (UUID, int) lives in handler code, not a binding layer.
- **Public health route:** `GET /healthz` is registered before any middleware that could fail (no JWT, no DB dependency); Fly's healthchecks must succeed even when Postgres is unreachable.

## Considered alternatives

- **`gin`.** Rejected: opinionated `*gin.Context`, custom middleware signature, and bundled binding/validation/rendering. Most popular Go router by stars, but popularity is not idiomaticity. The migration cost away from `gin` is real (every handler signature changes).
- **`echo`.** Rejected: same lock-in shape as `gin` with slightly cleaner ergonomics and a smaller community. No advantage over `chi` on the dimensions that matter here.
- **Stdlib `net/http` only (Go 1.22+ enhanced mux).** Rejected, narrowly. The new pattern matching closes most of the historical gap, but middleware composition and route grouping are still hand-rolled. We'd write `chi`-shaped helpers within three weeks. Defensible alternative for someone optimizing for "no dependencies"; not the right call when learning idiomatic Go middleware patterns is part of v0's purpose.
- **`gorilla/mux`.** Rejected: maintenance status was uncertain for years (de-archived in 2023, but the momentum has not returned). `chi` is the active idiomatic-Go router.
- **`fiber`.** Rejected: built on `fasthttp`, not `net/http`. The `net/http` ecosystem (middleware, observability, every third-party library) is the wrong thing to opt out of for v0.

## Consequences

- **One dependency.** `chi/v5` and its sub-package `chi/middleware` are the only router-side deps. `go.mod` stays small.
- **Handler shape locked to `http.Handler`.** Any team member or AI assistant joining later writes plain stdlib handlers; there is no framework-specific syntax to learn.
- **Validation, binding, rendering chosen later.** When the first endpoint with a non-trivial body lands (Commit, create event), we pick a validation approach (probably hand-rolled or `go-playground/validator`) on its own merits, not because the router shipped one.
- **Testing is `httptest.NewRequest` + `httptest.NewRecorder`.** No framework-specific test client. Integration tests against a real Postgres (per `CLAUDE.md`) hit the `chi.Router` directly.
- **No GraphQL, no gRPC.** This ADR commits to REST/JSON over HTTP. Out of scope for v0; revisit if we ever want a public API.
