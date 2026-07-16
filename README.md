# Aletheia API

API de certificação de conteúdo que ancora hashes criptográficos de
imagens em uma blockchain EVM para provar autoria e impedir que
conteúdo gerado por IA seja passado como real.

Para a visão completa do sistema, ver:

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): contêineres,
  componentes, modelo de dados e integração com a blockchain.
- [`docs/USAGE_FLOW.md`](docs/USAGE_FLOW.md): jornada do usuário,
  ciclo de vida do certificado e modos de verificação.
- [`docs/SEQUENCE_DIAGRAM.md`](docs/SEQUENCE_DIAGRAM.md): sequências
  detalhadas de certificação e verificação.

## Como funciona

1. **Certificar.** Uma fonte confiável envia uma imagem. A API calcula
   SHA-256, extrai pHash + descritores ORB + grade de cores LAB
   (128×128 médias por célula) via OpenCV, ancora o par
   `(contentHash, featureCommitment)` numa transação EVM e persiste o
   certificado em PostgreSQL. Nenhuma imagem é armazenada.
2. **Verificar.** Qualquer um pode consultar por hash
   (`GET /certificates/verify?hash=`) ou enviar uma imagem
   (`POST /certificates/verify`). O fluxo por imagem tenta primeiro o
   match exato por SHA-256 e, na ausência, cai num caminho de
   similaridade visual usando LSH em `phash_bands`, Hamming-256 e
   re-check ORB + resíduo de cor contra a grade de cores armazenada no
   certificado.

## Pré-requisitos

- Docker e Docker Compose v2 (recomendado); ou
- Go 1.22+, PostgreSQL 15+, OpenCV instalado no host (necessário para
  o `gocv`) e um endpoint JSON-RPC EVM (Anvil/Polygon).

## Subir com Docker (recomendado)

O `docker-compose.yml` sobe Postgres, Anvil, Jaeger e a API, já com as
envs cabeadas entre eles.

```bash
git clone https://github.com/waizbart/aletheia-api.git
cd aletheia-api
docker compose up --build
```

Endpoints disponíveis:

- API em `http://localhost:8080`
- Swagger UI em `http://localhost:8080/docs`
- Spec OpenAPI em `http://localhost:8080/docs/openapi.yaml`
- Health em `http://localhost:8080/health`
- Painel de observabilidade em `http://localhost:8080/observability`
- Jaeger UI em `http://localhost:16686`
- RPC Anvil em `http://localhost:8545`

Para derrubar:

```bash
docker compose down       # mantém volumes
docker compose down -v    # apaga pgdata
```

## Subir só a API local (Go) com a infra no Docker

Útil para iterar mais rápido sem rebuildar a imagem:

```bash
docker compose up -d postgres anvil
cp .env.example .env
go run ./cmd/api
```

O `.env.example` já aponta para `localhost` nas portas expostas pelo
compose.

## Subir sem Docker

1. Clone o repo e copie o template de env:

   ```bash
   git clone https://github.com/waizbart/aletheia-api.git
   cd aletheia-api
   cp .env.example .env
   ```

2. Edite `.env` com sua conexão Postgres, RPC EVM, endereço de
   transmissor (`FROM_ADDRESS`) e endereço do contrato âncora
   (`CONTRACT_ADDRESS`).

3. Rode as migrações na ordem:

   ```bash
   psql "$DATABASE_URL" -f migrations/001_create_certificates.sql
   psql "$DATABASE_URL" -f migrations/002_perceptual_v2.sql
   psql "$DATABASE_URL" -f migrations/003_phash_bands_and_commitment.sql
   psql "$DATABASE_URL" -f migrations/004_color_grid.sql
   ```

4. Suba a API:

   ```bash
   go run ./cmd/api
   ```

A porta vem de `SERVER_PORT` (default `8080`).

## Documentação da API

Swagger UI interativo em
[http://localhost:8080/docs](http://localhost:8080/docs) com o servidor
rodando. O spec OpenAPI 3.0 bruto fica em `/docs/openapi.yaml`.

## Observabilidade

A API instrumenta cada etapa dos fluxos de certificação e verificação e
expõe isso de duas formas:

- **Painel ao vivo** em
  [http://localhost:8080/observability](http://localhost:8080/observability):
  acompanha em tempo real (via Server-Sent Events) cada etapa do pipeline
  acendendo — `SHA-256 ➜ pHash ➜ ORB ➜ commitment ➜ blockchain ➜
  Postgres` na certificação, e `pHash variants ➜ LSH ➜ comparação de
  candidatos` na verificação. Mostra os valores reais de cada etapa, a
  latência por etapa e total, a decisão de verificação (distância de
  Hamming, inliers ORB, resíduo de cor LAB e o motivo de casar ou não por
  candidato) e um histórico das últimas requisições.
- **Spans OpenTelemetry** exportados via OTLP/HTTP para o Jaeger
  ([http://localhost:16686](http://localhost:16686)). Cada requisição vira
  um span pai com um span filho por etapa, carregando os mesmos atributos.

O export OTel é opcional: com `OTEL_EXPORTER_OTLP_ENDPOINT` em branco os
spans viram no-op e a API roda normalmente sem o Jaeger. O painel
funciona independentemente do OTel.

## Endpoints

### Health

```
GET /health
```

Devolve `200 OK` com `{ "status": "ok" }` quando o processo está no ar.

### Certificar imagem

```
POST /certificates
Content-Type: multipart/form-data

file: <arquivo de imagem>
X-Registrant: <opcional, identificador do registrante>
```

Tipos aceitos: JPEG, PNG, GIF, WebP, BMP, TIFF. Limite de 100 MB.

Resposta (`201 Created`):

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "content_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "registrant": "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD18",
  "tx_hash": "0x9fce0c2b9b0d2d8f3a1e5e7d4c5b3a8f7e6d4c3b2a1908f7e6d5c4b3a2918e7",
  "block_number": 0,
  "created_at": "2026-02-25T12:00:00Z"
}
```

Erros possíveis: `400` (arquivo ausente), `409` (já certificado), `413`
(acima de 100 MB), `415` (tipo não aceito), `422` (falha em extração,
anchor ou persistência).

> `block_number` é gravado como `0` hoje. O serviço de RPC não busca o
> receipt depois do `eth_sendTransaction`. Detalhes em
> [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

### Verificar por upload

```
POST /certificates/verify
Content-Type: multipart/form-data

file: <arquivo de imagem>
```

Tenta match exato por SHA-256 e, na ausência, cai no caminho de
similaridade visual (pHash variants ➜ LSH bands ➜ Hamming-256 ➜
re-check ORB nos top-K candidatos).

### Verificar por hash

```
GET /certificates/verify?hash=<sha256-hex>
```

Ambos os endpoints respondem com o mesmo formato:

`200 OK` quando certificado:

```json
{
  "certified": true,
  "certificate": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "content_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "registrant": "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD18",
    "tx_hash": "0x9fce0c2b9b0d2d8f3a1e5e7d4c5b3a8f7e6d4c3b2a1908f7e6d5c4b3a2918e7",
    "block_number": 0,
    "created_at": "2026-02-25T12:00:00Z"
  }
}
```

`404 Not Found` quando não certificado:

```json
{ "certified": false, "certificate": null }
```

### Remover certificado

```
DELETE /certificates/<sha256-hex>
```

Apaga o certificado pelo SHA-256. Todos os dados do certificado
(assinatura ORB, grade de cores, bandas de pHash) vivem no banco, então
a remoção é uma única operação atômica.

Respostas: `204 No Content` (sem corpo) em caso de sucesso; `404 Not
Found` quando não há certificado para o hash.

## Variáveis de ambiente

Todas obrigatórias salvo indicação em contrário. Ver `.env.example`.

| Variável | Descrição | Exemplo |
|----------|-----------|---------|
| `DATABASE_URL` | DSN do PostgreSQL | `postgres://aletheia:aletheia@localhost:5432/aletheia?sslmode=disable` |
| `RPC_URL` | Endpoint JSON-RPC EVM | `http://127.0.0.1:8545` |
| `FROM_ADDRESS` | Endereço EVM destravado usado por `eth_sendTransaction` | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` |
| `CONTRACT_ADDRESS` | Endereço do contrato âncora | `0x5FbDB2315678afecb367f032d93F642f64180aa3` |
| `SERVER_PORT` | Porta HTTP (opcional, default `8080`) | `8080` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Endpoint OTLP/HTTP do Jaeger (opcional; em branco desliga o export) | `jaeger:4318` |
| `OTEL_SERVICE_NAME` | Nome do serviço nos traces (opcional, default `aletheia-api`) | `aletheia-api` |
| `OBS_RING_CAPACITY` | Traces recentes mantidos em memória para o painel (opcional, default `50`) | `50` |

## Estrutura do projeto

```
cmd/api/              Entrypoint e wiring de dependências
internal/domain/      Entidades e lógica de negócio pura
internal/usecase/     Workflows de aplicação e ports (interfaces)
internal/handler/     Handlers HTTP, middleware, DTOs, Swagger
internal/repository/  Adapters PostgreSQL e EVM RPC
internal/feature/     Extrator OpenCV (ORB + grade de cores LAB)
internal/observability/ Recorder do pipeline, coletor SSE e ponte OpenTelemetry
internal/config/      Helpers de env
migrations/           SQL de criação e evolução do schema
docs/                 Diagramas e visões de arquitetura
```

## Testes

Unitários (sem dependências externas):

```bash
go test ./...
```

End-to-end (precisam de Postgres no ar). Suba a infra primeiro e ative
a build tag `e2e`:

```bash
docker compose up -d postgres
go test -tags e2e ./tests/e2e/...
```

## Licença

MIT
