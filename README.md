# Account Tracker — Backend

Go API for [Account Tracker](../account-tracker), an offline-first personal and
shared expense tracker. It does two things:

- **Cloud backup** — a manual, full-replace backup of everything a user owns,
  keyed by a secret UUID generated on the device. There are no accounts or
  logins.
- **Shared books** — books several people edit together, keyed by an 8-character
  share code. They sync as a CRDT: concurrent edits from any number of devices,
  online or offline, converge to the same state without overwriting each other.

The frontend owns the data model; the backend stores and merges what it sends.

## Stack

- Go 1.25, Gin, pgx v5
- PostgreSQL (Neon)
- golang-migrate (SQL embedded in the binary, applied on startup)
- Deployed on Vercel with the Go framework preset

## Running locally

Requires Go 1.25+ and a PostgreSQL database.

```bash
cat > .env <<'ENV'
DATABASE_URL=postgresql://user:pass@host/dbname
PORT=8080
# CORS_ORIGINS=https://example.com   # optional extra allowed origins, comma-separated
ENV

go run main.go          # http://localhost:8080 — migrations run on startup
```

Without a reachable `DATABASE_URL` the server still starts; `/ping` reports
`"db": "disconnected"` and data endpoints return 500.

```bash
go build ./... && go vet ./...
go test ./...           # unit tests, no database needed
gofmt -l .              # must print nothing
```

## API

```
GET  /ping                         health: {message, db, commit}

POST /api/sync/push-uuid           cloud backup (full replace)
GET  /api/sync/pull-uuid/:uuid     restore

POST /api/shared/v2                share a book → {code, version}
GET  /api/shared/v2/:code?since=N  read changes since version N
POST /api/shared/v2/:code/sync     push changes, receive everything newer

POST /api/shared/share             v1, kept for app versions not yet updated:
GET  /api/shared/:code               read-only once a book has been upgraded
PUT  /api/shared/:code               to v2 (PUT then answers 409)
```

Request and response shapes, the merge rules and the database schema are in
[`CLAUDE.md`](CLAUDE.md).

## Deployment

Pushing to `main` deploys to production on Vercel. `vercel.json` only selects the
Go framework preset: Vercel builds `main.go` and runs it as a server on `PORT`,
passing every request at its real path. Set `DATABASE_URL` (and optionally
`CORS_ORIGINS`) in the Vercel project. Migrations in
`internal/db/migrations/` are applied automatically on the next start.

## Project structure

```
main.go                     entry point (local and Vercel)
internal/app/               router, handlers: backup (sync.go), shared books v1 (share.go)
                            and v2 (share_v2.go), CRDT merge (crdt.go)
internal/db/                migration runner + SQL migrations
internal/middleware/        CORS allowlist (web + Capacitor origins)
```

## License

MIT
