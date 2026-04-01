# Account Tracker - Backend

The robust Go-based backend for the [Account Tracker](../account-tracker) frontend, responsible for data synchronization, user authentication, and managing collaborative shared books.

## 🚀 Built With

*   **Language**: [Go 1.25+](https://golang.org/)
*   **Web Framework**: [Gin (v1.12)](https://github.com/gin-gonic/gin)
*   **Database Driver**: [pgx (v5)](https://github.com/jackc/pgx)
*   **Migrations**: [golang-migrate](https://github.com/golang-migrate/migrate)
*   **Authentication**: [Google OAuth2](golang.org/x/oauth2) & [golang-jwt/jwt](github.com/golang-jwt/jwt/v5)
*   **Environment**: [godotenv](github.com/joho/godotenv)
*   **Deployment Target**: Designed for Vercel serverless functions (via `api/index.go`) or standalone execution.

## 🔑 Key Features

*   **Google OAuth Authentication**: Secure login flow redirecting users via Google and generating structured JWTs.
*   **Offline-First Synchronization (`/api/sync`)**: Bi-directional data sync matching the offline-first Capacitor frontend apps using `/push` and `/pull` endpoints.
*   **Collaborative Books (`/api/shared`)**: Dedicated RESTful endpoints specifically to create, fetch, and update shared spending accounts via unique share codes.
*   **Automated PostgreSQL Migrations**: Evaluates and applies database schema updates on connection.

## 🛠️ Getting Started

### Prerequisites

*   Go 1.25 or higher installed.
*   A running instance of PostgreSQL (Neon DB is heavily used as the default).

### Installation

1.  Clone the repository:
    ```bash
    git clone <repository-url>
    cd account-tracker-backend
    ```

2.  Install all Go dependencies:
    ```bash
    go mod download
    ```

3.  Set up environment variables by copying `.env.example` to `.env`:
    ```bash
    cp .env.example .env
    ```

### Environment Configuration

In your `.env` file, ensure you have the following keys configured:

```env
PORT=8080
DATABASE_URL=postgres://user:pass@host/dbname # Your PostgreSQL connection string
FRONTEND_URL=http://localhost:5173            # Used for redirecting after OAuth login
GOOGLE_CLIENT_ID=your_client_id               # Setup in Google Cloud Console
GOOGLE_CLIENT_SECRET=your_client_secret
JWT_SECRET=your_jwt_signing_secret
```

### Running Locally

**Debug mode (local dev):**
To start the Gin development server on the default port:
```bash
GIN_MODE=debug go run main.go
```

**Release mode:**
```bash
GIN_MODE=release go run main.go
```

The server will be reachable at `http://localhost:8080` (or whichever port specified in `.env`).

*On application start, the server will ping the configured database and automatically run any necessary migrations.*

## 🛣️ API Structure

*   `GET /ping` - Healthcheck endpoint
*   **Authentication**
    *   `GET /api/auth/google/login` - Initiates OAuth flow
    *   `GET /api/auth/google/callback` - OAuth callback handler & JWT generator
*   **Data Sync** *(Requires JWT Auth)*
    *   `POST /api/sync/push` - Receive data from client devices
    *   `GET /api/sync/pull` - Send latest validated state down to clients
*   **Shared Spaces**
    *   `POST /api/shared/share` - Generates a new unique 6-digit sharing code
    *   `GET /api/shared/:code` - Fetches shared book metadata
    *   `PUT /api/shared/:code` - Updates members/details of a shared book

## 📦 Project Structure

```text
account-tracker-backend/
├── api/                   # Vercel serverless function entrypoints (index.go)
├── internal/              # Private application code
│   ├── app/               # Main application and router setup (app.go)
│   ├── auth/              # JWT and Google OAuth logic
│   ├── db/                # Database connection and Migrations
│   └── middleware/        # Gin middlewares (e.g., AuthMiddleware)
├── main.go                # Standard entry point for self-hosting local dev
├── go.mod / go.sum        # Dependency management files
├── .env / .env.example    # Environment variables
└── vercel.json            # Vercel deployment configuration
```

## 📄 License

This project is licensed under the [MIT License](LICENSE) (Please review and apply as needed).
