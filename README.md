# Account Tracker — Backend

Go backend for [Account Tracker](../account-tracker). Handles Google OAuth, cloud data sync, and shared book collaboration.

## Tech Stack

- **Go 1.25** + **Gin v1.12**
- **PostgreSQL** (Neon DB) via **pgx v5**
- **golang-migrate v4** (auto-runs on startup)
- **Google OAuth2** + **JWT** (`golang-jwt/jwt v5`)
- **Vercel** serverless deployment (`api/index.go`)

## Getting Started

### Prerequisites

- Go 1.25+
- PostgreSQL instance (or [Neon DB](https://neon.tech) free tier)
- Google Cloud project with OAuth 2.0 credentials

### Setup

```bash
go mod download

cp .env.example .env
# fill in .env (see below)

go run main.go
# server runs at http://localhost:8080
```

### Environment Variables

```env
PORT=8080
DATABASE_URL=postgresql://user:pass@host/dbname
FRONTEND_URL=http://localhost:5173
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
GOOGLE_REDIRECT_URI=http://localhost:8080/api/auth/google/callback
JWT_SECRET=...
```

## API

```
GET  /ping

# Auth
GET  /api/auth/google/login
GET  /api/auth/google/callback

# Cloud sync (JWT required)
POST /api/sync/push
GET  /api/sync/pull

# UUID backup (no auth)
POST /api/sync/push-uuid
GET  /api/sync/pull-uuid/:uuid

# Shared books (no auth)
POST /api/shared/share
GET  /api/shared/:code
PUT  /api/shared/:code
```

Push sync = full replace (DELETE all + INSERT all). Pull sync = return all user data. The frontend is always the source of truth when pushing.

## Project Structure

```
account-tracker-backend/
├── main.go                          # Standalone dev entry point
├── api/index.go                     # Vercel serverless entry point
├── internal/
│   ├── app/
│   │   ├── app.go                   # Router setup, DB init, route registration
│   │   ├── sync.go                  # Sync push/pull handlers
│   │   └── share.go                 # Shared book handlers
│   ├── auth/
│   │   ├── google.go                # Google OAuth flow
│   │   └── jwt.go                   # JWT generation & validation
│   ├── db/
│   │   ├── migrate.go               # Migration runner
│   │   └── migrations/              # SQL migration files
│   └── middleware/
│       └── auth_middleware.go       # JWT validation middleware
└── .env / .env.example
```

See [`CLAUDE.md`](CLAUDE.md) for full architectural details including DB schema and key patterns.

## License

MIT
