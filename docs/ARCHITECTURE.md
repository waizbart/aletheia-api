# Arquitetura

Visão estática dos componentes da Aletheia API e suas integrações. O recorte
segue Clean Architecture: as dependências apontam para dentro
(handler → usecase → domain), e a infraestrutura (Postgres, RPC EVM, OpenCV,
atestação de plataforma) fica atrás de portas declaradas em
`internal/usecase/ports.go`.

## Visão de contêineres

```mermaid
flowchart LR
    subgraph Externo
        SDK["App de câmera /<br/>SDK parceiro"]
        PUB["Qualquer verificador<br/>(público)"]
        OP["Operador<br/>(curl, Swagger UI)"]
    end

    subgraph Aletheia["Aletheia API (Go 1.22)"]
        API["HTTP API<br/>net/http + ServeMux<br/>:8080"]
        WORK["Anchor worker<br/>(goroutine)"]
    end

    subgraph Infra["Infraestrutura"]
        DB[("PostgreSQL 16<br/>orgs · devices · certificates<br/>phash_bands · anchors")]
        CHAIN["EVM JSON-RPC<br/>(Anvil local, Polygon em prod)"]
        SC["AnchorRegistry.sol"]
    end

    SDK -- "captura assinada<br/>+ atestação de hardware" --> API
    PUB -- "verificação por hash<br/>ou por upload" --> API
    OP -- "onboarding, painel" --> API

    API -- "SQL via lib/pq<br/>pool de conexões" --> DB
    WORK -- "lê pendentes" --> DB
    WORK -- "eth_sendRawTransaction<br/>1 raiz Merkle por lote" --> CHAIN
    CHAIN -- "inclui tx no bloco" --> SC
```

Duas mudanças estruturais em relação ao desenho anterior: **a certificação não
fala mais com a blockchain** (isso virou trabalho do worker, fora do caminho da
requisição), e **a certificação é fechada** — só chega ao `CertifyUseCase` uma
captura cuja assinatura de dispositivo já foi conferida.

## Visão de componentes

```mermaid
flowchart TB
    subgraph cmd["cmd/api"]
        MAIN["main.go<br/>(composition root)"]
    end

    subgraph handler["internal/handler (Adapter)"]
        H_CAP["CaptureHandler<br/>POST /captures/nonce · /captures<br/>/devices · GET /usage"]
        H_CERT["CertificateHandler<br/>verify · delete · legacy certify"]
        H_ADM["AdminHandler<br/>/admin/orgs · /admin/keys"]
        MID["Auth · RateLimit · CORS<br/>Concurrency · Logging"]
    end

    subgraph usecase["internal/usecase (Application)"]
        UC_CAP["AttestedCapture · Enroll<br/>IssueNonce · RevokeDevice"]
        UC_C["Certify · Verify · Delete"]
        UC_ORG["CreateOrg · IssueAPIKey<br/>Authenticate · Usage"]
        UC_ANC["Anchor worker"]
        PORTS["Ports<br/>Certificate/Device/Nonce/Org/Usage/Anchor Repository<br/>BlockchainService · FeatureExtractor · AttestationVerifier"]
    end

    subgraph domain["internal/domain (Entity)"]
        ENT["Certificate · Device · Org · APIKey<br/>CaptureNonce · Anchor<br/>Merkle · CaptureSigningPayload<br/>FeatureSignature · PHash"]
    end

    subgraph infra["Infraestrutura"]
        PG["Postgres repos"]
        EVM["EVMAnchorService<br/>(secp256k1 · RLP · receipts)"]
        ATT["attestation.Registry<br/>AndroidVerifier"]
        ORB["OpenCVExtractor (gocv)"]
    end

    MAIN --> H_CAP & H_CERT & H_ADM & MID
    MAIN --> UC_CAP & UC_C & UC_ORG & UC_ANC
    MAIN --> PG & EVM & ATT & ORB

    H_CAP --> UC_CAP
    H_CERT --> UC_C
    H_ADM --> UC_ORG

    UC_CAP -. depende de .-> PORTS
    UC_C -. depende de .-> PORTS
    UC_ORG -. depende de .-> PORTS
    UC_ANC -. depende de .-> PORTS
    UC_CAP --> ENT
    UC_C --> ENT

    PG -. implementa .-> PORTS
    EVM -. implementa .-> PORTS
    ATT -. implementa .-> PORTS
    ORB -. implementa .-> PORTS
```

O pacote `internal/attestation` implementa a porta
`usecase.AttestationVerifier` devolvendo tipos de `domain`
(`AttestationRequest` / `AttestationEvidence`), então a camada de aplicação
nunca importa infraestrutura de atestação.

## Modelo de dados

```mermaid
erDiagram
    ORGS ||--o{ API_KEYS : "emite"
    ORGS ||--o{ DEVICES : "inscreve"
    ORGS ||--o{ USAGE_COUNTERS : "consome"
    ORGS ||--o{ CERTIFICATES : "possui"
    DEVICES ||--o{ CERTIFICATES : "captura"
    ANCHORS ||--o{ CERTIFICATES : "commita"
    CERTIFICATES ||--o{ PHASH_BANDS : "tem 0..N bandas"

    ORGS {
        uuid id PK
        text name
        text plan "developer|growth|enterprise"
        text status "active|suspended"
    }
    API_KEYS {
        uuid id PK
        uuid org_id FK
        text key_hash UK "SHA-256; o texto puro nunca é persistido"
        text key_prefix "primeiros caracteres, para exibição"
        timestamptz revoked_at
    }
    DEVICES {
        uuid id PK
        uuid org_id FK
        text platform "android|ios"
        bytea public_key "PKIX DER da chave atestada"
        text attestation_level "software|tee|strongbox"
        text status "active|revoked"
    }
    CAPTURE_NONCES {
        text value PK
        uuid org_id
        timestamptz expires_at
        timestamptz consumed_at "uso único"
    }
    CERTIFICATES {
        uuid id PK
        text content_hash UK "SHA-256 hex"
        bytea phash "32 bytes"
        bytea orb_descriptors
        bytea color_grid "128x128x3 médias LAB"
        bytea feature_commitment "32 bytes"
        uuid org_id FK
        uuid device_id FK "nulo = upload não atestado"
        timestamptz captured_at "reportado pelo dispositivo, coberto pela assinatura"
        uuid anchor_id FK
        bytea_array merkle_proof
        int leaf_index
        text tx_hash
        bigint block_number
    }
    ANCHORS {
        uuid id PK
        bytea root "raiz Merkle do lote"
        int leaf_count
        text tx_hash
        bigint block_number "real, vindo do receipt"
        text status "pending|confirmed|failed"
    }
    USAGE_COUNTERS {
        uuid org_id FK
        text operation
        text period "AAAA-MM em UTC"
        bigint count
    }
```

`phash_bands` materializa o prefilter LSH: cada certificado com pHash gera N
tuplas `(band_idx, band_value)`, e a verificação faz `UNNEST` das bandas das
rotações candidatas antes do recheque Hamming exato de 256 bits.

Nenhuma imagem é armazenada em lugar nenhum — só a grade de cores e os
descritores ORB, que bastam ao matcher.

## Integração com a blockchain

```mermaid
flowchart LR
    UC["AnchorUseCase"] -- "lê certificados<br/>sem âncora" --> DB[("Postgres")]
    UC --> TREE["BuildMerkleTree<br/>folhas duplo-hasheadas<br/>pares ordenados"]
    TREE --> SVC["EVMAnchorService"]
    SVC -- "assina localmente<br/>secp256k1 + EIP-155" --> RAW["eth_sendRawTransaction"]
    RAW --> NODE["EVM Node"]
    NODE -- "mina" --> SC["AnchorRegistry.anchor(root, count)"]
    SVC -- "eth_getTransactionReceipt<br/>(poll até confirmar)" --> NODE
    SVC -. "txHash, blockNumber real" .-> UC
    UC -- "grava âncora + prova<br/>de inclusão por certificado<br/>(uma transação SQL)" --> DB
```

Pontos que valem ter em mente:

- **Assinatura local.** `eth_sendTransaction` exigia uma conta destravada no
  nó, o que funciona no Anvil e em nenhum provedor RPC hospedado. Agora a
  transação é montada e assinada no processo (RLP + secp256k1, EIP-155) e
  transmitida já assinada.
- **`block_number` é real.** O serviço faz poll do receipt antes de gravar o
  lote, então o número de bloco registrado sempre corresponde a uma inclusão de
  fato. Antes era gravado `0` para todo certificado.
- **Uma transação por lote.** O custo de uma âncora não muda com a quantidade
  de certificados sob ela. Cada certificado guarda `leaf_index` e
  `merkle_proof`, e a verificação on-chain roda pela `MerkleProof` da
  OpenZeppelin.
- **Falha deixa o lote pendente.** Se a transação não confirma, nada é gravado
  e o próximo ciclo tenta de novo. Reancorar custa uma transação a mais;
  marcar certificados como ancorados numa raiz que nunca entrou distribuiria
  provas de nada.

## Deploy local (docker-compose.yml)

```mermaid
flowchart LR
    subgraph Host["Host do dev"]
        API_C["api:8080"]
        PG_C["postgres:5432"]
        ANVIL_C["anvil:8545"]
        JAEGER_C["jaeger:16686"]
    end

    API_C -- DATABASE_URL --> PG_C
    API_C -- RPC_URL --> ANVIL_C
    API_C -- OTLP --> JAEGER_C
    PG_C -. volume .- VOL1[("pgdata")]
```
