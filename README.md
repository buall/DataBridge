# DataBridge

DataBridge is a PostgreSQL-to-PostgreSQL migration MVP with a React UI and a Go migration engine.

## Persistence

SQLite persistence is enabled by default:

- Database: `data/databridge.db`
- AES-256-GCM credential key: `data/databridge.key`

The key file is generated on first start with user-only permissions. Keep the database and key together when backing up; encrypted passwords cannot be recovered without the key.

Override their locations with:

```bash
DATABRIDGE_DB_PATH=/secure/path/databridge.db \
DATABRIDGE_KEY_PATH=/secure/path/databridge.key \
npm run dev:api
```

## What works

- Test and keep PostgreSQL instance connections in API process memory.
- Discover databases and user tables from PostgreSQL system catalogs.
- Select source/target databases and individual tables.
- Create schemas, enum types, sequences and table definitions.
- Stream table data through PostgreSQL binary `COPY` without buffering a full table in memory.
- Recreate primary keys, unique/check/foreign-key constraints and indexes.
- Synchronize sequence values.
- Poll live task progress, events and per-table results.
- Validate exact source/target row counts and show a report.
- Cancel a running task.

## MVP boundaries

- The target database must already exist.
- Views, materialized views, functions, procedures, extensions, roles and grants are not migrated.
- Custom PostgreSQL types other than enums are not handled yet.
- Tasks and credentials are in memory and disappear when the API restarts.
- One migration runs inside one API process; there is no distributed worker or resume checkpoint yet.
- Use this version for small databases and controlled test environments before production data.

## Run

Install dependencies and build the frontend:

```bash
npm install
npm run build
```

Start the API and production frontend on port `8081`:

```bash
npm run dev:api
```

Open [http://127.0.0.1:8081](http://127.0.0.1:8081).

For frontend development, run the API and Vite separately:

```bash
npm run dev:api
npm run dev
```

Vite proxies `/api` to `127.0.0.1:8081` by default. Override it with:

```bash
VITE_API_PROXY_TARGET=http://127.0.0.1:9000 npm run dev
```

## Verification

```bash
GOCACHE="$PWD/.gocache" go test ./...
GOCACHE="$PWD/.gocache" go vet ./...
npm run build
```
