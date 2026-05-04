# Polling for pins, SSE for chat, push for async — no WebSocket layer

v0's realtime architecture is intentionally split by use case rather than unified behind a single streaming primitive:

- **Pin counts** on the map are **polled** via `GET /events?bbox=...&since=...` every 5–10 seconds while the Map tab is foregrounded. Counts animate smoothly client-side to give the *feel* of liveness.
- **Chat messages** use **SSE** (Server-Sent Events) for receive: `GET /chats/:id/stream` is opened on chat-screen mount and closed on dismount. Sending is plain `POST /chats/:id/messages`.
- **Async awareness** (background app, other tabs, off-screen events) is handled by **APNS push notifications** plus on-foreground refresh of in-app badge counts.

Supabase Realtime is **not used.** WebSocket is **not used.**

## Why

Two realisations forced the unified-WebSocket plan apart.

**The wedge needs the *feel* of liveness, not technical real-time.** The PRD calls out users seeing counts tick up live ("3/6 → 4/6 → 5/6") as load-bearing. Polling at 5–10s with smooth client-side count-up animation captures ~95% of that emotional payoff at a fraction of the architectural cost. Pokémon Go itself is mostly polled snapshots with rich animation, not WebSocket-pushed state.

**Streaming connections are only useful when the user is staring at the relevant screen.** Chat needs sub-second latency *only* when the user is actively viewing a chat. When they are not, push notifications (APNS) are how iOS apps deliver async chat awareness — that is how iMessage and most platform-native chat experiences work. There is no value in maintaining an always-on streaming connection for a screen the user is not currently looking at.

Following these realisations through:
- The streaming layer's scope collapses from "every active user, viewport-filtered, multiple subscriptions" to "users currently viewing a specific chat, one stream per chat." At v0 peak this is roughly 30–80 concurrent streams, not 300–600.
- That smaller scope makes building it ourselves in the Go server cheap (3–5 days) rather than expensive (2–3 weeks).
- It also makes SSE the right protocol — chat receive is naturally one-way over the stream, and SSE is dramatically simpler than WebSocket: it is just HTTP, plays cleanly with all HTTP infrastructure, has built-in auto-reconnect with `Last-Event-ID`, and can be `curl`-ed for debugging.
- And it makes Supabase Realtime unnecessary, which removes ~$30–50/mo of recurring cost and one SDK from the iOS client.

## Considered alternatives

- **Supabase Realtime (WebSocket) for both pins and chat.** Rejected: viewport-filtered pin subscriptions add re-subscription churn on map pan, server-side cost scales linearly with concurrent users (Supabase bills per delivered message), and the always-on connection model is wrong-shaped for chat where push notifications cover the non-foreground case anyway.
- **Custom WebSocket layer in the Go server for both.** Rejected: WebSocket's bidirectionality is wasted on pins (one-way push) and unnecessary on chat (POST + stream is fine). More complex than SSE for no benefit at this shape and scale.
- **WebTransport.** Rejected: no first-party iOS client, immature server tooling, and the protocol's wins (datagrams, multiple streams, low handshake latency) target use cases (multiplayer games, live video, AR/VR) we do not have.
- **Polling for everything, including chat.** Rejected: chat at 2–3s polling intervals feels noticeably worse than the iMessage/Slack baseline users expect.
- **Always-on app-level SSE stream for in-app badge updates.** Deferred: APNS + on-foreground refresh covers the v0 bar. Reconsider if user testing reveals stale badges feel broken.

## Consequences

- **Pin counts have ~5–10s update latency.** Acceptable for the wedge if client-side animation is good. If it tests as feeling stale, drop the interval to 3s before adding a streaming path.
- **The Go server owns the SSE chat fan-out.** Implementation: a Postgres `LISTEN/NOTIFY` consumer feeding per-chat subscriber sets, writing SSE frames to subscribed connections. New chat-message rows trigger `NOTIFY chat_message, '{event_id, message_id}'`; the consumer routes to interested connections.
- **Chat reconnection requires monotonic message IDs.** The `messages` table needs a per-chat monotonic sequence so SSE clients reconnecting with `Last-Event-ID` can request a replay of missed messages. Design for this from day one.
- **APNS push fan-out is on the v0 critical path.** It is the only mechanism for async chat awareness. Lives in the Go server, triggered by the same `LISTEN/NOTIFY` events that drive SSE fan-out.
- **No Supabase Realtime SDK on iOS.** One less dependency, one less cost line. Supabase's role narrows to Postgres + Auth + Storage.
- **Streaming connections are short-lived and tied to a single screen** (chat). No app-level always-on stream. Connection lifecycle: open on chat-screen mount, close on dismount or app background. iOS will close them automatically on background anyway, but explicit close is cleaner.
