---
name: Backend Coding Conventions (Go + Gin + pgx)
description: Professional Go coding standards for the backend — covering project structure, error handling, database patterns, API design, and idiomatic Go practices.
---

# Backend Coding Conventions (Go)

These rules are **MANDATORY** for every change to this Go backend codebase. All code must be idiomatic Go and follow established community standards (Effective Go, Go Code Review Comments, Uber Go Style Guide).

---

## 1. Project Structure — Clean Architecture

### 1.1 Package Layout
```
internal/
  app/        # Router setup, dependency wiring, application bootstrap
  auth/       # Authentication logic (OAuth, JWT)
  db/         # Database layer: connection pool, queries, migrations
  middleware/ # Gin middlewares (auth guard, CORS, logging, recovery)
  handler/    # HTTP handlers (one file per resource domain)
  service/    # Business logic (one file per domain; NO HTTP concerns)
  repository/ # Data access layer (one file per domain; NO business logic)
  model/      # Struct definitions for DB rows, DTOs, request/response types
```

### 1.2 Dependency Flow (STRICT)
```
handler → service → repository → db
```
- **Handlers** parse HTTP requests, call services, write HTTP responses. NO SQL here.
- **Services** contain business logic. They accept repository interfaces (for testability). NO `*gin.Context` here.
- **Repositories** execute database queries. They return domain models. NO business decisions here.
- **NEVER** skip layers (e.g., handler calling repository directly). This causes tight coupling.

### 1.3 Interface-Based Decoupling
- Define interfaces **at the consumer side**, not the provider side (Go convention).
- Services depend on repository interfaces, not concrete structs:
```go
// ✅ Correct: interface defined in the service package
type RecordRepository interface {
    GetByID(ctx context.Context, id int64) (*model.Record, error)
    Create(ctx context.Context, r *model.Record) error
}

type RecordService struct {
    repo RecordRepository
}
```

---

## 2. Error Handling — Be Explicit

### 2.1 Core Rules
- **ALWAYS** check every returned `error`. Never use `_` to discard errors in production code.
- Wrap errors with context using `fmt.Errorf("operation description: %w", err)`.
- Return errors; let the **handler** decide the HTTP status code. Services MUST NOT write responses.

### 2.2 Custom Errors
- Define domain-specific error types or sentinel errors for expected failures:
```go
var (
    ErrRecordNotFound = errors.New("record not found")
    ErrUnauthorized   = errors.New("unauthorized")
)
```
- Use `errors.Is()` and `errors.As()` for error matching. **NEVER** compare error strings.

### 2.3 Panic Policy
- **NEVER** use `panic()` in application code. Only acceptable in `init()` for truly unrecoverable boot failures.
- Gin's recovery middleware catches panics — but relying on it is sloppy engineering.

---

## 3. Context (`context.Context`)

- **Every** function that performs I/O (DB, HTTP calls, file access) MUST accept `ctx context.Context` as its **first parameter**.
- Pass the request context from Gin all the way down: `c.Request.Context()`.
- Set appropriate timeouts for DB and external API calls using `context.WithTimeout()`.
- **NEVER** store `context.Context` in a struct field. Pass it as a function argument.

---

## 4. Database (pgx/v5 + golang-migrate)

### 4.1 Connection Management
- Use `pgxpool.Pool` (connection pool), never single `pgx.Conn` in production.
- Initialize the pool once at application startup and inject it via dependency injection.

### 4.2 Query Safety
- **ALWAYS** use parameterized queries (`$1, $2, ...`). **NEVER** build SQL with `fmt.Sprintf` or string concatenation — SQL injection risk.
```go
// ✅ Correct
row := pool.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", userID)

// ❌ FORBIDDEN
query := fmt.Sprintf("SELECT name FROM users WHERE id = %d", userID)
```

### 4.3 Transaction Management
- Use `pool.BeginTx()` with proper `defer tx.Rollback()` and explicit `tx.Commit()`.
- If a service method requires multiple writes, it should receive a `pgx.Tx` or start its own transaction.

### 4.4 Migrations
- All schema changes go through `golang-migrate` migration files in `internal/db/migrations/`.
- Migrations have **both** `up` and `down` files. Down migrations must be **reversible**.
- **NEVER** modify existing applied migrations. Create new ones.

---

## 5. API Design (Gin)

### 5.1 Handler Structure
```go
func (h *RecordHandler) CreateRecord(c *gin.Context) {
    // 1. Parse & validate request
    var req CreateRecordRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 2. Call service
    record, err := h.service.Create(c.Request.Context(), &req)
    if err != nil {
        // 3. Map domain errors to HTTP status
        handleError(c, err)
        return
    }

    // 4. Return response
    c.JSON(http.StatusCreated, record)
}
```

### 5.2 Request/Response Types
- Define separate structs for requests and responses — NEVER expose database models directly.
- Validate all input using Gin's binding tags (`binding:"required"`) or custom validators.

### 5.3 Consistent Response Format
```json
// Success
{ "data": { ... } }

// Error
{ "error": "human readable message" }
```

### 5.4 Route Organization
- Group routes by resource: `/api/records`, `/api/auth`, `/api/books`.
- Apply middleware at the group level, not per-route (unless specifically needed).

---

## 6. Naming Conventions (Idiomatic Go)

| Concern | Convention | Example |
|---|---|---|
| Packages | short, lowercase, singular | `auth`, `model`, `handler` |
| Exported types | PascalCase | `RecordService`, `User` |
| Unexported helpers | camelCase | `buildQuery`, `parseToken` |
| Interfaces | verb-er or descriptive | `RecordRepository`, `TokenValidator` |
| Constants | PascalCase (exported) or camelCase | `MaxRetries`, `defaultTimeout` |
| Files | snake_case | `record_handler.go`, `auth_middleware.go` |
| Test files | `*_test.go` | `record_service_test.go` |
| Acronyms | ALL CAPS in names | `userID`, `ParseJWT`, `httpURL` |

### 6.1 Receiver Naming
- Use **1-2 letter abbreviations** of the type name:
```go
func (s *RecordService) Create(...)  // ✅
func (self *RecordService) Create(...) // ❌ not idiomatic Go
func (this *RecordService) Create(...) // ❌ not idiomatic Go
```

---

## 7. Code Quality Rules

### 7.1 Formatting
- Run `gofmt` / `goimports` on save. Non-negotiable.
- Max line length: strive for ≤120 characters, hard limit 150.

### 7.2 Comments
- Exported functions, types, and constants **MUST** have Go doc comments starting with the name.
- Unexported helpers need comments only if the logic is non-obvious.

### 7.3 Dependencies
- Only use well-maintained, popular libraries. Verify on pkg.go.dev before importing.
- Keep `go.mod` clean — run `go mod tidy` after changes.

### 7.4 Testing
- Write table-driven tests for business logic (`service` layer).
- Use interfaces for mocking — no reflection-based mock libraries unless necessary.
- Test file location: same package, `*_test.go`.

---

## 8. Security

- **Secrets**: NEVER hardcode secrets. Always read from environment variables via `os.Getenv()` or `.env` with `godotenv`.
- **JWT**: Validate all claims (expiration, issuer, audience). Never trust the client blindly.
- **CORS**: Configure allowed origins explicitly — never use wildcard `*` in production.
- **Input sanitization**: Validate and sanitize all user input before processing.
