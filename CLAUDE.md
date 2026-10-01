# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Backend for the offline-first expense tracker in `../account-tracker` (Vue 3 PWA
+ Capacitor). It has two jobs: a manual full-replace **cloud backup** keyed by a
secret client UUID, and **shared books** keyed by a share code. There are no
accounts, no auth, and no business logic beyond storing and merging what the
frontend sends — the frontend owns the data model (see its `CLAUDE.md`).

## Stack

- **Language**: Go 1.25 (`go.mod`); `mise.toml` pins the local toolchain to 1.26.1
- **Framework**: Gin v1.12, `gin-contrib/cors`
- **Database**: PostgreSQL (Neon) via pgx v5 (`pgxpool`)
- **Migrations**: golang-migrate v4, SQL embedded with `//go:embed`, auto-run on startup
- **Auth**: None. Backup is keyed by a secret client UUID; shared books by a share code.
- **Deploy**: Vercel serverless (`api/index.go`) or standalone (`main.go`)

## Commands

```bash
go run main.go                    # local dev (port 8080, loads .env)
GIN_MODE=release go run main.go   # release mode
go build ./... && go vet ./...    # build + vet
go test ./...                     # unit tests (internal/app; no DB needed)
gofmt -l .                        # must print nothing
```

There is no `.env.example` despite the README; create `.env` from the variables
below. `tmp_server` in the repo root is a gitignored local build artifact.

## File Map

```
account-tracker-backend/
├── main.go                  # Standalone entry: app.GetRouter().Run(":$PORT")
├── api/index.go             # Vercel entry: Handler(w, r) → app.GetRouter().ServeHTTP()
├── vercel.json              # rewrites /api/(.*) and /ping → /api/index.go
├── internal/
│   ├── app/
│   │   ├── app.go           # GetRouter (sync.Once): initDB (pool, ping, migrate) + routes
│   │   ├── sync.go          # UUID backup push/pull, execPipelined, date normalisers, JSONB helpers
│   │   ├── share.go         # share code generation, v1 shared-book get/create/merge (+ tombstones)
│   │   ├── crdt.go          # v2 pure functions: Doc/Entity/Register, parseDoc (validation), mergeDoc, filterSince, flatten
│   │   ├── share_v2.go      # v2 handlers: create, get ?since, sync (tx + FOR UPDATE)
│   │   └── *_test.go        # merge properties/rules, flatten, v1 merge + tombstones, router resolution, JSONB round trip
│   ├── db/
│   │   ├── migrate.go       # RunMigrations(pgx5:// URL) — closes its own connections
│   │   └── migrations/      # 000001_init_schema, 000002_shared_spaces, 000003_multi_currency, 000004_crdt_sync (+ .down)
│   └── middleware/
│       └── cors.go          # origin allowlist (web + Capacitor) + CORS_ORIGINS
└── .agent/skills/           # older agent conventions — see "Conventions" below
```

## Environment Variables

```env
PORT=8080                                   # standalone only
DATABASE_URL=postgresql://user:pass@host/dbname
CORS_ORIGINS=https://example.com            # optional, extra comma-separated origins
```

`godotenv.Load()` runs inside `initDB`; a missing `.env` only logs. A missing or
bad `DATABASE_URL` does **not** stop startup — `dbPool` stays `nil`, every
data handler returns 500 `"Database not connected"`, and `/ping` reports
`db: "disconnected"`. On Vercel, `VERCEL_GIT_COMMIT_SHA` is echoed by `/ping`.

## API Routes

```
GET  /ping                         # {message, db, commit}

POST /api/sync/push-uuid           # body: {uuid, profile?, books, records, personal_records, categories, templates}
GET  /api/sync/pull-uuid/:uuid     # same shape back (minus uuid); 404 if the uuid has never backed up

# v1 (legacy clients)
POST /api/shared/share             # body: {book, records} → {code}
GET  /api/shared/:code             # stored payload verbatim (flatten(doc) once upgraded); 404 if unknown
PUT  /api/shared/:code             # body: {book, records, deletedIds, deletedMemberIds} → MERGE → {status:"ok"}
                                   #   409 {error:"upgrade_required"} if the space has a v2 doc

# v2 (CRDT, see "Shared-book sync v2")
POST /api/shared/v2                # body: {doc} → {code, version:1}
GET  /api/shared/v2/:code?since=N  # → {version, doc: entities with _v > N}; legacy space → {version:0, legacy: payload}
POST /api/shared/v2/:code/sync     # body: {since, changes, base?} → {version, doc: entities with _v > since}
                                   #   legacy space without a current base → 409 {error:"base_required", legacy, legacyHash}
```

The v2 group is `sharedGrp.Group("/v2")`: Gin ≥1.8 lets the static `v2`
segment coexist with v1's `/:code` wildcard (a code like `v2ABCDEF` still hits
v1; real codes are uppercase anyway). `router_test.go` asserts every route
resolves to its handler — keep it green when touching routes.

JSON keys are camelCase inside entities (`bookId`, `paidById`, `splitAmongIds`,
`sourceBookId`, `isDefault`, `createdAt`, `userId`) but the top-level backup
arrays are snake_case `personal_records`. Match the frontend exactly — unknown
keys are silently ignored by `ShouldBindJSON`, so a typo drops data instead of
erroring.

Multi-currency fields (all optional — old clients and old backups have none):
`profile.baseCurrency`; `books[].currency`; `templates[].currency`;
`records[]` and `personal_records[]` `amountCurrency`, `original`, `booked`,
`fx`; plus `records[].splitCustomAmounts`. The currency strings are nullable
TEXT (`*string` + `omitempty`). `original` / `booked` / `fx` /
`splitCustomAmounts` are frontend-owned JSON objects stored as JSONB and
returned verbatim (`json.RawMessage` + `omitempty`); only "is an object" is
checked (400 otherwise), never the inner shape. The rule is absent on push → SQL
NULL → absent on pull: `jsonbArg` maps a missing field *and* an explicit JSON
`null` to SQL NULL (pgx would store `"null"` bytes as JSONB `null`), and
`jsonbValue` maps NULL back to nil. `profile` is omitted on pull when
`users.base_currency` is NULL.

The UUID endpoints are unauthenticated by design: the client UUID is a secret,
unguessable capability token (v4 UUID). It must never be exposed — in
particular it is NOT the id embedded in shared-book member lists (the frontend
uses a separate `memberId`).

## Database Schema

### Core tables (migration 000001)

| Table | Key columns |
|-------|-------------|
| `users` | `id UUID PK`, `email TEXT UNIQUE NOT NULL`, `name NOT NULL`, `google_id`, `password_hash`, `avatar_url`, `theme` (last four unused) |
| `categories` | `id UUID PK`, `user_id FK→users CASCADE`, `name`, `type`, `icon`, `color`, `is_default` |
| `books` | `id UUID PK`, `user_id FK→users CASCADE`, `name`, `created_at` |
| `book_members` | `id UUID PK`, `book_id FK→books CASCADE`, `name`, `user_id FK→users SET NULL` |
| `records` | `id UUID PK`, `book_id FK→books`, `type`, `amount DECIMAL(15,2)`, `category TEXT`, `date DATE`, `note`, `paid_by_id FK→book_members SET NULL`, `split_among_ids JSONB` |
| `personal_records` | `id UUID PK`, `user_id FK→users`, `type`, `amount`, `category`, `date DATE`, `note`, `source_book_id UUID` (no FK) |
| `record_templates` | `id UUID PK`, `user_id FK→users`, `name`, `type`, `amount` (nullable), `category`, `note` |

Migration 000003 (multi-currency) adds only nullable columns with no defaults,
so "absent" stays NULL:

| Table | Added columns |
|-------|---------------|
| `records` | `amount_currency TEXT`, `original JSONB`, `booked JSONB`, `fx JSONB`, `split_custom_amounts JSONB` |
| `personal_records` | `amount_currency TEXT`, `original JSONB`, `booked JSONB`, `fx JSONB` |
| `books` | `currency TEXT` |
| `record_templates` | `currency TEXT` |
| `users` | `base_currency TEXT` (the backup uuid's `profile.baseCurrency`) |

`type` is `CHECK (type IN ('expense','income'))`. Every id column is `UUID`, so
a non-UUID id from the client (or an empty string) fails the whole push.
`category` is free text: records store the category *name*, templates store the
category *id* — the backend does not care, but don't normalise one into the
other here.

### Shared spaces (migration 000002)

| Table | Key columns |
|-------|-------------|
| `shared_spaces` | `code TEXT PK` (8-char, older 6-char still valid), `payload JSONB` (full book+records snapshot), `updated_at` |

`shared_spaces.payload` stores `{book, records, deletedIds, deletedMemberIds}`. `updateSharedBookHandler` parses it to merge; `share`/`get` treat it as an opaque blob. The frontend owns that schema, so shared books keep every field, including ones the backup tables have no column for.

Migration 000004 (CRDT sync):

| Table | Added columns |
|-------|---------------|
| `shared_spaces` | `doc JSONB` (v2 CRDT doc; **NULL = legacy v1 space, not upgraded**), `version BIGINT NOT NULL DEFAULT 0` |
| `book_members` | `archived BOOLEAN` (nullable; `SyncMember.Archived *bool`, absent → NULL → absent like 000003) |

For an upgraded space `payload` is rewritten as `flatten(doc)` on every v2 write, so v1 GETs keep working read-only.

New migrations: add `00000N_name.up.sql` + `.down.sql` in `internal/db/migrations/`; they are embedded at build time and applied on the next cold start. Make them idempotent (`IF NOT EXISTS`) — a failed migration only logs, and the server keeps serving against the old schema.

## Key Patterns

### UUID backup (push/pull)

Push = **full replace** inside one transaction: upsert the `users` row → DELETE all the UUID's rows (children first) → INSERT everything from the body. All statements are queued and sent via `execPipelined` in chunks of 500 per round trip; per-row round trips to Neon previously made large backups time out. **Statement order matters** (FKs: `book_members` before the `records` that reference them) — append new statements in dependency order. A failed statement aborts the whole push with 500 `"Failed to insert into <table>: …"`.

Ownership guards: `book_members` and `records` inserts use `INSERT … SELECT … WHERE EXISTS (book owned by this uuid)`, and every `ON CONFLICT DO UPDATE` is scoped with `WHERE <table>.user_id = $2`, so a payload cannot write into another user's rows even with a colliding id. A guarded insert that matches nothing is a silent no-op, not an error.

Members with a `userId` (the other person's public `memberId`) get their own `users` row (`<id>@anonymous.local`) because `book_members.user_id` is an FK.

**The backup is lossy by schema.** The typed `Sync*` structs only carry stored columns. Since 000003, `splitCustomAmounts` and the currency fields are persisted (000004: member `archived`); the push still drops `Book.shareCode` and the frontend's `isSynced`, so after a restore shared books are unlinked. Persisting a new field needs a migration, the struct field, the INSERT *and* its `ON CONFLICT` SET list, and the pull SELECT/Scan (keep Scan order = SELECT order). The users upsert is `ON CONFLICT DO UPDATE SET base_currency` — a push without `profile.baseCurrency` clears it, like every other full-replace field.

Pull = SELECT all rows for this UUID. Only a missing users row (or a malformed uuid) is a 404; any other DB error returns 500 via `pullError` (never a partial/empty 200) so the client cannot mistake a failure for "cloud is empty" and wipe local data on the next push. Dates are formatted `YYYY-MM-DD`; nullable UUIDs come back as `""` via `COALESCE(...::text, '')`. Member queries run after the books cursor is closed so the pull never holds two of the pool's 2 connections.

`normalizeTimestamp` / `normalizeDate` accept Go-`time.String()`-style timestamps from older clients and coerce them to RFC3339 / `YYYY-MM-DD`.

### Shared-book merge (v1)

`updateSharedBookHandler` MERGES the pusher's snapshot into the stored payload instead of overwriting it (`mergeSharedPayload`). It runs in one transaction with `SELECT payload, doc … FOR UPDATE`, so concurrent PUTs serialize; if `doc` is non-NULL it answers 409 `upgrade_required` and writes nothing.

- tombstones persist: stored `deletedIds` / `deletedMemberIds` = union of stored and incoming, and every tombstoned record/member is dropped — **including ones in the incoming snapshot**, so a stale client cannot resurrect them;
- records unioned by id, incoming wins (whole object, by arrival order); tombstoned ids removed;
- `book` is shallow-merged: a copy of the stored book with every key present in the incoming book overlaid (incoming wins for present keys, absent keys are kept — so an older client that doesn't know `book.currency` cannot erase it); then members are unioned by id with the stored ones, except ids in `deletedMemberIds`, (accumulated) which are removed. The merge never drops a member on its own, which is why the client must send `deletedMemberIds` explicitly. Records are not field-merged — clients preserve unknown record fields themselves. Input maps are never mutated.

Record edits are still whole-object "incoming wins", so a stale v1 client can still clobber a newer edit of the same record — that is what v2 fixes. A stored payload that fails to unmarshal is treated as empty. Share codes are 8 chars from a 32-symbol alphabet without `O/0/I/1` (~40 bits); on an insert collision it regenerates once. Codes are case-sensitive in the DB — the frontend uppercases before joining.

### Shared-book sync v2 (CRDT)

A state-based CRDT: a map of last-writer-wins registers keyed by hybrid-logical-clock strings. The server is schema-agnostic apart from the flatten conventions below. Pure functions live in `crdt.go` (unit-tested for commutativity/associativity/idempotence); handlers in `share_v2.go`.

Wire format:

- Register `{"v": <any JSON>, "t": "<clock>"}` — `t` non-empty, ≤ 64 bytes, compared byte-wise (Go `a > b`); `"0"` is the lowest clock in practice (data converted from v1).
- Entity `{"f": {<immutable fields>}, "r": {<name>: Register}, "_v": <int64>}` — `_v` is server-owned (space version at which the entity last changed); incoming `_v` and any incoming key starting with `_` are ignored.
- Doc `{"book": Entity, "members": {<id>: Entity}, "records": {<id>: Entity}}`; a change set may omit `book` or carry only some entities/registers. Responses omit `book` unless its `_v > since`; `members`/`records` are always objects.

Merge (`mergeDoc`, never mutates inputs): a register is taken if the stored one is missing or `incoming.t > stored.t` (strict; ties keep stored); an `f` key is set only if absent (first writer wins, never overwritten). An entity changed iff a register was taken or an `f` key added; only changed entities get `_v = newVersion`. The CRDT laws hold because clients never emit two values with one clock nor disagree on an `f` value. Validation (400): `t` rules above, `r` values must be objects with both `v` and `t`, entity ids 1–64 bytes, ≤ 5000 members / 20000 records (checked on input and on the merged doc), body ≤ 4 MB.

Sync = one transaction: `SELECT payload, doc, version … FOR UPDATE` → if `doc` is NULL, start from the client's `base` (its deterministic conversion of the legacy payload; every entity counts as changed) — but only if its `baseOf` equals `legacyHash(payload)` checked under the row lock (`baseMatchesPayload`); otherwise 409 `base_required` with the current payload and hash. That check is what stops an old client's v1 PUT, landing between a client's legacy GET and its upgrade, from being overwritten by a stale base (and the v1 client can't resend: it gets 409 `upgrade_required` from then on). `base` is ignored once a doc exists (two concurrent upgraders from the same payload send identical bases) → merge `changes` at `version + 1` → only if something changed, UPDATE `doc`, `version`, `payload = flatten(doc)`, `updated_at` → commit → return entities with `_v > since`. No change = no write, version unchanged. Create stamps every entity `_v = 1`, `version = 1`.

`flatten(doc)` → v1 payload `{book, records, deletedIds}` (generic per entity: copy `f`, then each register in name order — `$name` with an object value is spread into the output, anything else becomes `output[name] = v`; numbers kept verbatim):

- `book` = flatten(book) + `members` = every member flattened, sorted by `f.created` (string) then id — archived members included (they carry `archived: true`);
- `records` = flattened records sorted by id, excluding those whose flattened `deleted` is `true` (those ids go to `deletedIds`); the `deleted` key is removed from every record.

### Router singleton

`GetRouter()` uses `sync.Once` — safe for both Vercel (cold start per instance) and standalone. The pgx pool is capped at `MaxConns=2` because every serverless instance owns its own pool and Neon's connection limit is shared; migrations open and close their own connection (`pgx5://` scheme is swapped in by `initDB`).

### CORS

Origins are matched with `AllowOriginFunc` rather than `AllowOrigins`: gin-contrib/cors panics at startup on a non-`http(s)://` entry such as Capacitor's `capacitor://localhost`, which took the whole API down once. The allowlist no longer depends on `GIN_MODE` (unset on Vercel). A new frontend origin must be added to `allowedOrigins()` or `CORS_ORIGINS`.

## Conventions

Match the existing code: handlers in `internal/app` talk to `dbPool` directly, return `gin.H{"error": …}` with an HTTP status, and use `c.Request.Context()` for every query. `.agent/skills/coding-conventions/SKILL.md` describes a handler/service/repository layout and Google/JWT auth that this codebase does not have (auth was removed); treat it as general Go guidance, not a description of the repo, and don't restructure to fit it as part of an unrelated change. `README.md` is likewise stale (OAuth, JWT, `/api/sync/push`) — trust this file and the code.
