# Arquitetura

Visão estática dos componentes da Aletheia API e suas integrações. O
recorte segue Clean Architecture: as dependências apontam para dentro
(handler → usecase → domain), e a infraestrutura (Postgres, S3, RPC
EVM, OpenCV) fica atrás de portas declaradas em
`internal/usecase/ports.go`.

## Visão de contêineres

```mermaid
flowchart LR
    subgraph Externo
        FE["Front-end /<br/>Cliente HTTP"]
        OP["Operador<br/>(curl, Swagger UI)"]
    end

    subgraph Aletheia["Aletheia API (Go 1.22)"]
        API["HTTP API<br/>net/http + ServeMux<br/>:8080"]
    end

    subgraph Infra["Infraestrutura"]
        DB[("PostgreSQL 16<br/>certificates<br/>phash_bands")]
        S3[("S3 / MinIO<br/>bucket aletheia-images")]
        CHAIN["EVM JSON-RPC<br/>(Anvil local, Sepolia em prod)"]
        SC["Smart Contract<br/>âncora de hash"]
    end

    FE -- "REST/JSON<br/>multipart upload" --> API
    OP -- "Swagger /docs<br/>/health" --> API

    API -- "SQL via lib/pq<br/>pool de conexões" --> DB
    API -- "PutObject /<br/>GetObject (AWS SDK v2)" --> S3
    API -- "eth_sendTransaction<br/>(calldata = hash‖commit)" --> CHAIN
    CHAIN -- "inclui tx no bloco" --> SC
```

## Visão de componentes

```mermaid
flowchart TB
    subgraph cmd["cmd/api"]
        MAIN["main.go<br/>(composition root)"]
    end

    subgraph handler["internal/handler (Adapter)"]
        H_CERT["CertificateHandler<br/>POST /certificates<br/>POST/GET /certificates/verify"]
        H_DOCS["DocsHandler<br/>/docs (Swagger UI)"]
        H_HEAL["HealthHandler<br/>/health"]
        MID["LoggingMiddleware"]
        DTO["DTOs / response helpers"]
    end

    subgraph usecase["internal/usecase (Application)"]
        UC_C["CertifyUseCase"]
        UC_V["VerifyUseCase"]
        PORTS["Ports<br/>CertificateRepository<br/>BlockchainService<br/>FeatureExtractor<br/>ImageBlobStore"]
    end

    subgraph domain["internal/domain (Entity)"]
        ENT["Certificate<br/>FeatureSignature<br/>FeatureCommitment<br/>PHash + PHashBands<br/>HashContent / Hamming256<br/>ErrAlreadyCertified"]
    end

    subgraph repo["internal/repository (Infra)"]
        PG["PostgresCertificateRepo"]
        EVM["RPCBlockchainService"]
        BLOB["S3BlobStore"]
        FACT["BlockchainServiceFromEnv"]
    end

    subgraph feature["internal/feature (Infra)"]
        ORB["OpenCVExtractor (gocv)"]
    end

    subgraph config["internal/config"]
        CFG["env helpers"]
    end

    MAIN --> H_CERT & H_DOCS & H_HEAL & MID
    MAIN --> UC_C & UC_V
    MAIN --> PG & EVM & BLOB & ORB & FACT
    MAIN --> CFG

    H_CERT --> UC_C
    H_CERT --> UC_V
    H_CERT --> DTO

    UC_C -. depende de .-> PORTS
    UC_V -. depende de .-> PORTS
    UC_C --> ENT
    UC_V --> ENT

    PG -. implementa .-> PORTS
    EVM -. implementa .-> PORTS
    BLOB -. implementa .-> PORTS
    ORB -. implementa .-> PORTS
```

Regra de dependência (Clean Architecture):
`handler → usecase → domain`, com `repository` e `feature`
implementando as portas declaradas em `usecase`. O único ponto que
conhece todas as camadas é `cmd/api/main.go`, onde a injeção é
cabeada.

## Modelo de dados

```mermaid
erDiagram
    CERTIFICATES ||--o{ PHASH_BANDS : "tem 0..N bandas"
    CERTIFICATES {
        uuid id PK
        text content_hash UK "SHA-256 hex"
        bytea phash "32 bytes, nullable (gravado em todo certificado novo)"
        bytea orb_descriptors "ORB binário, nullable"
        bytea orb_keypoints "keypoints serializados"
        text image_blob_key "chave no S3, nullable"
        bytea feature_commitment "32 bytes, bundle off-chain"
        text registrant "X-Registrant"
        text tx_hash "tx EVM"
        bigint block_number
        timestamptz created_at
    }
    PHASH_BANDS {
        uuid cert_id FK
        smallint band_idx "índice da banda LSH"
        smallint band_value "valor da banda"
    }
```

`phash_bands` materializa o prefilter LSH. Cada certificado com pHash
gera N tuplas `(band_idx, band_value)`. A verificação faz `UNNEST`
das bandas das rotações candidatas, une por `(band_idx, band_value)` e
reduz o universo antes do re-check Hamming exato de 256 bits.

## Integração com a blockchain

```mermaid
flowchart LR
    UC["CertifyUseCase"] --> SVC["RPCBlockchainService"]
    SVC -- "POST JSON-RPC<br/>eth_sendTransaction" --> NODE["EVM Node<br/>(Anvil / Sepolia)"]
    NODE -- "minera + inclui em bloco" --> CHAIN[("Blockchain")]
    NODE -. "retorna txHash" .-> SVC
    SVC -. "txHash, blockNum" .-> UC

    SVC -. "calldata = 64 bytes<br/>contentHash ‖ commitment" .-> NODE
```

Pontos atuais que valem ter em mente:

- O serviço não chama função ABI. Envia calldata bruta para o endereço
  em `CONTRACT_ADDRESS`. O contrato lê a calldata direto do bloco como
  prova de existência.
- `block_number` ainda é gravado como `0`. A resposta de
  `eth_sendTransaction` só devolve `txHash`; recuperar o bloco real
  exige `eth_getTransactionReceipt` adicional.
- `IsHashRegistered` é stub (`return false, nil`). Hoje a única fonte
  de verdade consultada é o Postgres.

## Deploy local (docker-compose.yml)

```mermaid
flowchart LR
    subgraph Host["Host do dev"]
        API_C["api:8080"]
        PG_C["postgres:5432"]
        ANVIL_C["anvil:8545"]
        MINIO_C["minio:9000<br/>console:9001"]
        INIT["minio-init<br/>(cria bucket)"]
    end

    API_C -- DATABASE_URL --> PG_C
    API_C -- RPC_URL --> ANVIL_C
    API_C -- S3_ENDPOINT --> MINIO_C
    INIT --> MINIO_C

    PG_C -. volume .- VOL1[("pgdata")]
    MINIO_C -. volume .- VOL2[("miniodata")]
```
