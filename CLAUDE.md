# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack

- **Language**: Go 1.25
- **Framework**: Gin v1.12
- **Database**: PostgreSQL (Neon DB) via pgx v5
- **Migrations**: golang-migrate v4 (auto-runs on startup)
- **Auth**: None. No accounts/login. Cloud backup is keyed by a secret client UUID; shared books by a share code.
- **Deploy**: Vercel serverless (`api/index.go`) or standalone (`main.go`)

## Commands

```bash
go run main.go                    # local dev (port 8080, loads .env)
GIN_MODE=release go run main.go   # release mode
go mod download                   # install deps
```

## File Map

```
account-tracker-backend/
├── main.go                          # Standalone entry: loads .env, calls app.GetRouter().Run()
├── api/
│   └── index.go                     # Vercel serverless entry: Handler(w, r) → app.GetRouter().ServeHTTP()
├── internal/
│   ├── app/
│   │   ├── app.go                   # Router init (once.Do), DB init (pool sizing), CORS, route registration
│   │   ├── sync.go                  # pushSyncByUUIDHandler, pullSyncByUUIDHandler (UUID cloud backup)
│   │   └── share.go                 # shareBookHandler, getSharedBookHandler, updateSharedBookHandler (merge)
│   ├── db/
│   │   ├── migrate.go               # RunMigrations(dbURL) — auto-runs on app init, closes its own conn
│   │   └── migrations/
│   │       ├── 000001_init_schema.up.sql    # Core tables
│   │       └── 000002_shared_spaces.up.sql  # shared_spaces table
│   └── middleware/
│       └── cors.go                   # CORS allowlist (web + Capacitor origins; CORS_ORIGINS env for extras)
└── .env / .env.example              # Environment config
```

## Environment Variables

```env
PORT=8080
DATABASE_URL=postgresql://user:pass@host/dbname
CORS_ORIGINS=https://example.com   # optional, extra comma-separated allowed origins
```

## API Routes

```
GET  /ping                         # healthcheck → {message, db}

POST /api/sync/push-uuid           # body: {uuid, books, records, ...} — full replace of that UUID's data
GET  /api/sync/pull-uuid/:uuid     # return all of that UUID's data (500 on DB error, never partial)

POST /api/shared/share             # create share code → stores full payload as JSONB
GET  /api/shared/:code             # fetch payload by code
PUT  /api/shared/:code             # MERGE payload by code (records by id + deletedIds; members unioned + deletedMemberIds)
```

The UUID sync endpoints are unauthenticated by design: the client UUID is a secret,
unguessable capability token (v4 UUID). It must never be exposed — in particular it is
NOT the id embedded in shared-book member lists (the frontend uses a separate `memberId`).

## Database Schema

### Core tables (migration 000001)

| Table | Key columns |
|-------|-------------|
| `users` | `id UUID PK`, `google_id TEXT UNIQUE`, `email`, `name`, `avatar_url` |
| `books` | `id UUID PK`, `user_id FK→users`, `name`, `created_at` |
| `book_members` | `id UUID PK`, `book_id FK→books`, `name`, `user_id FK→users (nullable)` |
| `records` | `id UUID PK`, `book_id FK→books`, `type`, `amount`, `category`, `date DATE`, `note`, `paid_by_id FK→book_members`, `split_among_ids JSONB` |
| `personal_records` | `id UUID PK`, `user_id FK→users`, `type`, `amount`, `category`, `date DATE`, `note`, `source_book_id UUID (nullable)` |
| `record_templates` | `id UUID PK`, `user_id FK→users`, `name`, `type`, `amount`, `category`, `note` |
| `categories` | `id UUID PK`, `user_id FK→users`, `name`, `type`, `icon`, `color`, `is_default` |

### Shared spaces (migration 000002)

| Table | Key columns |
|-------|-------------|
| `shared_spaces` | `code TEXT PK` (8-char, older 6-char still valid), `payload JSONB` (full book+records snapshot), `updated_at` |

`shared_spaces.payload` stores `{book, records}`. `updateSharedBookHandler` parses it to merge (see Key Patterns); `share`/`get` treat it as an opaque blob. The frontend owns the schema.

## Key Patterns

### UUID sync model (push/pull)
Push = **full replace** inside one transaction: DELETE all the UUID's rows → INSERT everything from the request body. A failed DELETE or INSERT aborts the whole push (no partial commit). Inserts of books/members/records are guarded so a payload can only write into books owned by that UUID. The frontend is the source of truth when pushing.

Pull = SELECT all rows for this UUID. Any DB error returns 500 (never a partial/empty 200) so the client cannot mistake a failure for "cloud is empty" and wipe local data on the next push.

### Anonymous users
UUID-based sync creates a `users` row with `id = uuid` and `email = uuid@anonymous.local` (inside the push transaction). `users.google_id` / `avatar_url` columns still exist in the schema but are unused.

### Shared-book merge
`updateSharedBookHandler` MERGES the pusher's snapshot into the stored payload instead of overwriting it: records are unioned by id (incoming wins), ids listed in the request's `deletedIds` are removed, and book members are unioned by id — except ids in `deletedMemberIds`, which are removed the same way. This prevents two members editing concurrently from clobbering each other. Share codes are 8 chars (older 6-char codes still resolve).

### Router singleton
`GetRouter()` uses `sync.Once` — safe for both Vercel (cold start per request) and standalone (persistent process). The pgx pool is capped (`MaxConns=2`) for serverless; migrations close their own connection.

### `isSynced` field (frontend-only)
The frontend sends `isSynced` on record objects; Go's JSON decoder ignores unknown fields, so no backend changes are needed for this field.
