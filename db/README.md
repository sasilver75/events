# db/

Schema and migrations live under [`/server/db/`](../server/db/), colocated
with the Go server that owns them
([ADR 0017](../docs/adr/0017-migration-tool-golang-migrate.md)). This
top-level directory exists as a pointer; nothing should be added here.
