# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack

- **Language**: Go 1.25
- **Framework**: Gin v1.12
- **Database**: PostgreSQL (Neon DB) via pgx v5
- **Migrations**: golang-migrate v4 (auto-runs on startup)
- **Auth**: Google OAuth2 + JWT (`golang-jwt/jwt v5`)
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
│   │   ├── app.go                   # Router init (once.Do), DB init, CORS, all route registration
│   │   ├── sync.go                  # pushSyncHandler, pullSyncHandler, pushSyncByUUIDHandler, pullSyncByUUIDHandler
│   │   └── share.go                 # shareBookHandler, getSharedBookHandler, updateSharedBookHandler
│   ├── auth/
│   │   ├── google.go                # GetGoogleLoginURL, GetGoogleUserInfo (OAuth2 flow)
│   │   └── jwt.go                   # GenerateJWT, ParseJWT (claims: google_id, email, name)
│   ├── db/
│   │   ├── migrate.go               # RunMigrations(dbURL) — auto-runs on app init
│   │   └── migrations/
│   │       ├── 000001_init_schema.up.sql    # Core tables
│   │       └── 000002_shared_spaces.up.sql  # shared_spaces table
│   └── middleware/
│       └── auth_middleware.go        # JWT validation → sets "user_google_id" in Gin context
└── .env / .env.example              # Environment config
```

## Environment Variables

```env
PORT=8080
DATABASE_URL=postgresql://user:pass@host/dbname
FRONTEND_URL=http://localhost:5173     # OAuth callback redirect target
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
GOOGLE_REDIRECT_URI=http://localhost:8080/api/auth/google/callback
JWT_SECRET=...
```

## API Routes

```
GET  /ping                         # healthcheck → {message, db}

GET  /api/auth/google/login        # redirect to Google OAuth consent
GET  /api/auth/google/callback     # receive code → upsert user → JWT → redirect to FRONTEND_URL/login?token=...&id=...&name=...&email=...&avatar=...

POST /api/sync/push-uuid           # public, body: {uuid, books, records, ...}
GET  /api/sync/pull-uuid/:uuid     # public

POST /api/sync/push                # JWT required — full replace of user's data
GET  /api/sync/pull                # JWT required — return all user's data

POST /api/shared/share             # create share code → stores full payload as JSONB
GET  /api/shared/:code             # fetch payload by 6-char code
PUT  /api/shared/:code             # overwrite payload by code
```

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
| `shared_spaces` | `code TEXT PK` (6-char), `payload JSONB` (full book+records snapshot), `updated_at` |

`shared_spaces.payload` is an opaque JSONB blob — the backend does not inspect its structure. The frontend owns the schema.

## Key Patterns

### Sync model (push/pull)
Push = **full replace**: DELETE all user's rows across all tables → INSERT everything from request body. No partial updates. This means the frontend is always the source of truth when pushing.

Pull = SELECT all rows for this user, return as structured JSON.

### Anonymous users
UUID-based sync creates a `users` row with `id = uuid` and `email = uuid@anonymous.local`. This allows the same push/pull flow without Google auth.

### Router singleton
`GetRouter()` uses `sync.Once` — safe for both Vercel (cold start per request) and standalone (persistent process).

### OAuth flow
`googleCallbackHandler` → upserts user → generates JWT with `google_id` as subject → redirects to `FRONTEND_URL/login?token=...&id=<internal_user_id>&...`

JWT middleware extracts `google_id` → push/pull handlers look up `users.id` from `google_id` internally.

### `isSynced` field (frontend-only)
The frontend sends `isSynced` on record objects; Go's JSON decoder ignores unknown fields, so no backend changes are needed for this field.

### Stub handlers
`registerHandler`, `loginHandler`, `getRecordsHandler`, `syncRecordsHandler` in `app.go` are stubs from early development — not used by the frontend.
