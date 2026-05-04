# db/

Database migrations and seed data for the Spur Supabase Postgres instance.

Populated by [issue #7](https://github.com/sasilver75/events/issues/7):
schema baseline (`users`, `events`, `commits`) and RLS read policies, and by
[issue #8](https://github.com/sasilver75/events/issues/8): hand-curated event
seed.

DB triggers are reserved for mechanical concerns (NOTIFY emission,
denormalized counts). Business rules live in the Go server — see
[ADR 0005](../docs/adr/0005-supabase-data-plane-go-server-business-logic.md)
and [`CLAUDE.md`](../CLAUDE.md).
