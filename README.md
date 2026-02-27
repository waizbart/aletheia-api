# Aletheia API TESTE

Content certification API that uses cryptographic hashing and blockchain registration to prove the authenticity of images and videos, preventing false AI-generated content from being passed off as real.

## How It Works

1. **Certify** — A trusted source uploads an image or video. The API computes a SHA-256 hash, registers it on an EVM-compatible blockchain, and stores the certificate metadata in PostgreSQL.
2. **Verify** — Anyone can upload an image/video or provide a hash to check whether it has been certified. The API looks up the hash and returns the certificate details if it exists.

## Prerequisites

- Docker and Docker Compose **or** Go 1.22+ with PostgreSQL 15+
- Access to an EVM-compatible JSON-RPC endpoint (e.g. Sepolia testnet)

## Quick Start (Docker)

1. Clone the repository and copy the environment template:

```bash
git clone https://github.com/waizbart/aletheia-api.git
cd aletheia-api
cp .env.example .env
```

1. Edit `.env` with your RPC URL, private key, and contract address (database is configured automatically by Compose).

2. Start everything:

```bash
docker compose up --build
```

This spins up PostgreSQL and the API. The database migration runs automatically on first start. The API is available at `http://localhost:8080`.

To stop:

```bash
docker compose down
```

To stop and remove the database volume:

```bash
docker compose down -v
```

## Manual Setup (without Docker)

1. Clone the repository and copy the environment template:

```bash
git clone https://github.com/waizbart/aletheia-api.git
cd aletheia-api
cp .env.example .env
```

1. Edit `.env` with your database connection string, RPC URL, private key, and deployed contract address.

2. Run the database migration:

```bash
psql "$DATABASE_URL" -f migrations/001_create_certificates.sql
```

1. Start the server:

```bash
go run ./cmd/api
```

The server listens on the port defined by `SERVER_PORT` (default `8080`).

## API Documentation

Interactive Swagger UI is available at [http://localhost:8080/docs](http://localhost:8080/docs) when the server is running. The raw OpenAPI 3.0 spec is served at `/docs/openapi.yaml`.

## API Endpoints

### Health Check

```
GET /health
```

Returns `200 OK` when the server is running.

### Certify Content

```
POST /certificates
Content-Type: multipart/form-data

Form field: "file" (image or video)
```

**Response** (`201 Created`):

```json
{
  "id": "uuid",
  "content_hash": "sha256-hex",
  "tx_hash": "0x...",
  "block_number": 12345,
  "created_at": "2026-02-25T12:00:00Z"
}
```

### Verify Content

By file upload:

```
POST /certificates/verify
Content-Type: multipart/form-data

Form field: "file" (image or video)
```

By hash:

```
GET /certificates/verify?hash=<sha256-hex>
```

**Response** (`200 OK` if found, `404 Not Found` if not):

```json
{
  "certified": true,
  "certificate": {
    "id": "uuid",
    "content_hash": "sha256-hex",
    "registrant": "0x...",
    "tx_hash": "0x...",
    "block_number": 12345,
    "created_at": "2026-02-25T12:00:00Z"
  }
}
```

## Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://user:pass@localhost:5432/aletheia?sslmode=disable` |
| `RPC_URL` | EVM JSON-RPC endpoint | `https://rpc.sepolia.org` |
| `PRIVATE_KEY` | Hex-encoded private key for signing transactions | `abc123...` |
| `CONTRACT_ADDRESS` | Deployed certification contract address | `0x...` |
| `SERVER_PORT` | HTTP server port | `8080` |

## Project Structure

```
cmd/api/              Entrypoint and dependency wiring
internal/domain/      Entities and pure business logic
internal/usecase/     Application workflows and port interfaces
internal/handler/     HTTP handlers and middleware
internal/repository/  PostgreSQL and blockchain adapters
migrations/           SQL migration files
```

## License

MIT
