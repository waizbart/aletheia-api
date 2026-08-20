# Aletheia API

API de proveniência de mídia. Fotos capturadas por um dispositivo inscrito
carregam prova criptográfica de que uma chave em hardware seguro assinou
**exatamente aqueles bytes** naquele momento — e essa prova sobrevive à
internet estragar a imagem.

Duas metades deliberadamente assimétricas:

- **Certificar é exato e fechado.** Só o app de câmera ou um parceiro embutindo
  o SDK certifica. O dispositivo gera uma chave dentro do elemento seguro,
  prova isso ao servidor uma vez na inscrição e, daí em diante, assina cada
  captura com ela.
- **Verificar é tolerante e aberto.** Qualquer um verifica, de graça. O
  pipeline perceptual reencontra o certificado a partir de um screenshot, de um
  reenvio no WhatsApp ou de um recorte — casos em que metadados embutidos já
  teriam sido removidos há muito tempo.

Para a visão completa do sistema:

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): contêineres, componentes,
  modelo de dados e integração com a blockchain.
- [`docs/ATTESTATION.md`](docs/ATTESTATION.md): o protocolo de captura
  atestada, o que cada verificação garante e o que ela não garante.
- [`docs/OPERATIONS.md`](docs/OPERATIONS.md): configuração, onboarding de
  clientes, worker de ancoragem e runbook.
- [`docs/USAGE_FLOW.md`](docs/USAGE_FLOW.md): jornada do usuário e ciclo de
  vida do certificado.
- [`docs/SEQUENCE_DIAGRAM.md`](docs/SEQUENCE_DIAGRAM.md): sequências detalhadas.
- [`docs/DATASET_GENERATION.md`](docs/DATASET_GENERATION.md): geração do
  dataset de benchmark.

## O que o sistema prova — e o que não prova

Vale dizer explicitamente, porque a diferença decide o que dá para vender:

**Prova.** Que uma chave guardada no elemento seguro de um dispositivo genuíno,
com bootloader travado e rodando um app assinado por uma chave conhecida,
assinou exatamente aqueles bytes em resposta a um desafio emitido pelo servidor
— e que os bytes não mudaram desde então. Quando um certificado é contestado, o
sistema diz exatamente qual organização e qual dispositivo respondem por ele.

**Não prova.** Que a cena era verdadeira, nem que o sensor gerou aqueles bytes.
Quem segura a chave é o app, então um app comprometido num dispositivo genuíno
pode assinar bytes que a câmera nunca viu; as verificações encarecem chegar
nessa posição, não a eliminam. E uma câmera atestada apontada para um monitor
exibindo uma imagem gerada produz um certificado válido de uma foto falsa. Esse
ataque — *rephotography* — não está resolvido por ninguém no setor, e as
mitigações (moiré, resposta ao flash, profundidade) são uma corrida
armamentista. Aletheia vende **atribuição e responsabilização**, não detecção
de IA.

## Como funciona

1. **Desafio.** O SDK pede um nonce de uso único (`POST /captures/nonce`).
2. **Inscrição, uma vez por dispositivo.** O dispositivo gera uma chave no
   TEE/StrongBox vinculada ao desafio e envia a cadeia de atestação
   (`POST /devices`). O servidor valida a cadeia até uma raiz de hardware do
   Google, confere o desafio, exige chave gerada em hardware, bootloader
   travado e app assinado por chave conhecida — e então fixa a chave pública.
3. **Captura.** O dispositivo assina `SHA-256(bytes) ‖ nonce ‖ metadados` com
   aquela chave e envia a imagem (`POST /captures`). O servidor confere a
   assinatura contra a chave fixada, extrai pHash + descritores ORB + grade de
   cores LAB via OpenCV e persiste o certificado. **Nenhuma imagem é
   armazenada.**
4. **Ancoragem.** Um worker em segundo plano agrupa os certificados pendentes
   sob uma única raiz Merkle e a grava na blockchain. Cada certificado guarda
   sua prova de inclusão, então qualquer um confere contra a raiz on-chain sem
   confiar nesta API.
5. **Verificação.** Por hash (`GET /certificates/verify?hash=`) ou por upload
   (`POST /certificates/verify`). O upload tenta primeiro o match exato por
   SHA-256 e, na ausência, cai num caminho de similaridade visual usando LSH em
   `phash_bands`, Hamming-256 e recheque ORB + resíduo de cor.

## Pré-requisitos

- Docker e Docker Compose v2 (recomendado); ou
- Go 1.22+, PostgreSQL 15+, OpenCV instalado no host (necessário para o `gocv`)
  e um endpoint JSON-RPC EVM (Anvil/Polygon).

## Subir com Docker (recomendado)

```bash
git clone https://github.com/waizbart/aletheia-api.git
cd aletheia-api
docker compose up --build
```

Endpoints disponíveis:

- API em `http://localhost:8080`
- Swagger UI em `http://localhost:8080/docs`
- Health em `http://localhost:8080/health`
- Painel de observabilidade em `http://localhost:8080/observability`
  (exige o token de admin)
- Jaeger UI em `http://localhost:16686`
- RPC Anvil em `http://localhost:8545`

O compose sobe **sem** raízes de atestação: as três variáveis
`ANDROID_*` vêm comentadas, a API funciona e verifica normalmente, e a inscrição
de dispositivos responde `501` até você fornecê-las. Para exercitar a inscrição
localmente, coloque o bundle em `config/android-attestation-roots.pem` e
descomente as três linhas no `docker-compose.yml`. Ver
[`config/README.md`](config/README.md).

O `CONTRACT_ADDRESS` do compose é o endereço determinístico do primeiro deploy
no Anvil, mas nada no compose faz esse deploy: sem publicar o
`AnchorRegistry`, o worker envia transações para um endereço sem código e a
ancoragem não ancora nada. Certificar e verificar não dependem disso.

Para derrubar:

```bash
docker compose down       # mantém volumes
docker compose down -v    # apaga pgdata
```

## Subir só a API local (Go) com a infra no Docker

```bash
docker compose up -d postgres anvil
cp .env.example .env
go run ./cmd/api
```

## Subir sem Docker

1. Clone o repo e copie o template de env:

   ```bash
   git clone https://github.com/waizbart/aletheia-api.git
   cd aletheia-api
   cp .env.example .env
   ```

2. Edite `.env`. No mínimo: `DATABASE_URL`, `ADMIN_API_TOKEN`, `RPC_URL`,
   `CONTRACT_ADDRESS`, `ANCHOR_PRIVATE_KEY` e `CHAIN_ID`.

3. Rode as migrações na ordem:

   ```bash
   for f in migrations/*.sql; do psql "$DATABASE_URL" -f "$f"; done
   ```

4. Suba a API:

   ```bash
   go run ./cmd/api
   ```

## Autenticação

Três níveis, deliberadamente distintos:

| Nível | Credencial | Alcança |
|---|---|---|
| Público | nenhuma | `GET /health`, `/docs`, verificação |
| Tenant | `Authorization: Bearer alk_…` | captura, inscrição de dispositivos, uso |
| Admin | `Authorization: Bearer <ADMIN_API_TOKEN>` | criar organizações e chaves, apagar certificados, painel |

O onboarding não é self-service de propósito: um registrante só vale alguma
coisa se alguém o avaliou, e essa avaliação é o diferencial. Criar organização
e emitir chaves são ações de operador.

Apenas o hash SHA-256 de uma chave de API é armazenado. O texto puro existe uma
única vez, na resposta da chamada que a criou.

## Endpoints

### Onboarding (admin)

```
POST   /admin/orgs                 { "name": "Acme", "plan": "developer" }
POST   /admin/orgs/{id}/keys       { "name": "ci" }      -> devolve a chave uma vez
DELETE /admin/keys/{id}
```

### Captura atestada (tenant)

```
POST /captures/nonce               -> { "nonce", "expires_at" }

POST /devices                      { "platform", "nonce", "cert_chain": [...], "model" }
POST /devices/{id}/revoke          { "reason" }

POST /captures                     multipart/form-data
                                     file, device_id, nonce, signature (base64),
                                     captured_at (RFC 3339), model, os_version, app_version

GET  /usage                        -> consumo do período corrente
```

Tipos aceitos: JPEG, PNG, GIF, WebP, BMP, TIFF. Limite de 100 MB.

Erros de captura: `402` (cota do plano esgotada), `403` (assinatura não
confere, ou dispositivo revogado), `404` (dispositivo não inscrito), `409`
(desafio já usado, ou conteúdo já certificado).

### Verificação (público)

```
GET  /certificates/verify?hash=<sha256-hex>
POST /certificates/verify          multipart/form-data com `file`
```

`200 OK` quando certificado:

```json
{
  "certified": true,
  "certificate": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "content_hash": "e3b0c442…",
    "registrant": "9f1c…",
    "attested": true,
    "device_id": "7b2e…",
    "captured_at": "2026-08-12T15:04:05.123456789Z",
    "created_at": "2026-08-12T15:04:06Z",
    "anchor": {
      "tx_hash": "0x9fce…",
      "block_number": 63481022,
      "leaf_index": 17,
      "merkle_proof": ["0x…", "0x…"]
    }
  }
}
```

`404 Not Found` quando não certificado: `{ "certified": false, "certificate": null }`.

O campo `attested` é o mais importante para quem verifica: distingue uma
captura de câmera de um upload comum. `anchor` só aparece depois que o worker
ancora o lote.

### Remover certificado (admin)

```
DELETE /certificates/<sha256-hex>
```

### Upload não atestado (legado, desligado por padrão)

`POST /certificates` aceita um upload sem proveniência de captura. Fica
**desligado** salvo `ALLOW_UNATTESTED_CERTIFY=true`, e responde `410 Gone`
quando desligado. Existe apenas como rota de migração para integrações
anteriores ao SDK — um upload que ninguém endossou é exatamente o que a captura
atestada substitui.

## Variáveis de ambiente

Ver [`.env.example`](.env.example), que documenta cada variável e por que ela
tem o valor padrão que tem. As essenciais:

| Variável | Descrição |
|----------|-----------|
| `DATABASE_URL` | DSN do PostgreSQL |
| `ADMIN_API_TOKEN` | Token das rotas de admin. Em branco **rejeita tudo**, não libera |
| `RPC_URL` / `CONTRACT_ADDRESS` | Endpoint EVM e `AnchorRegistry` implantado |
| `ANCHOR_PRIVATE_KEY` / `CHAIN_ID` | Conta que assina as ancoragens e a rede |
| `ANDROID_ATTESTATION_ROOTS` | Bundle PEM das raízes de hardware do Google |
| `ANDROID_ALLOWED_PACKAGES` | Application IDs autorizados a inscrever |
| `ANDROID_SIGNATURE_DIGESTS` | SHA-256 hex dos certificados de assinatura do APK |
| `ALLOW_UNATTESTED_CERTIFY` | Reabre `POST /certificates`. Padrão `false` |

## Contrato

[`contracts/AnchorRegistry.sol`](contracts/AnchorRegistry.sol) registra raízes
Merkle de forma append-only, emite um evento por lote e expõe `verify` sobre a
biblioteca `MerkleProof` da OpenZeppelin. Raízes nunca são removidas ou
sobrescritas: uma ancoragem afirma que um conjunto de certificados existia num
instante, e permitir edição tornaria a afirmação inútil.

## Estrutura do projeto

```
cmd/api/                 Entrypoint e wiring de dependências
contracts/               AnchorRegistry.sol
internal/domain/         Entidades e lógica de negócio pura
internal/usecase/        Workflows de aplicação e ports (interfaces)
internal/attestation/    Verificação de Android Key Attestation
internal/handler/        Handlers HTTP, middleware, DTOs, Swagger
internal/repository/     Adapters PostgreSQL e EVM
internal/feature/        Extrator OpenCV (ORB + grade de cores LAB)
internal/observability/  Recorder do pipeline, coletor SSE e ponte OpenTelemetry
internal/config/         Helpers de env
migrations/              SQL de criação e evolução do schema
docs/                    Diagramas e visões de arquitetura
```

## Testes

Unitários (sem dependências externas):

```bash
go test ./internal/... ./tests/...
bash scripts/check-coverage.sh
```

End-to-end (precisam de Postgres no ar):

```bash
docker compose up -d postgres
go test -tags e2e ./tests/e2e/...
```

## Observabilidade

- **Painel ao vivo** em `/observability` (atrás do token de admin): acompanha
  em tempo real cada etapa do pipeline via SSE, com valores reais, latência por
  etapa e a decisão de verificação por candidato.
- **Spans OpenTelemetry** exportados via OTLP/HTTP para o Jaeger. Com
  `OTEL_EXPORTER_OTLP_ENDPOINT` em branco os spans viram no-op e a API roda
  normalmente sem o Jaeger.

## Licença

MIT
