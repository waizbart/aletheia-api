# Diagramas de Sequência

Fluxos atuais da Aletheia API, conforme implementados em
`internal/usecase` e suas portas (`internal/usecase/ports.go`).

## Certificação (`POST /certificates`)

```mermaid
sequenceDiagram
    actor U as Cliente
    participant API as HTTP Handler
    participant UC as CertifyUseCase
    participant CV as OpenCV Extractor
    participant DB as PostgreSQL
    participant S3 as S3 / MinIO
    participant SC as EVM JSON-RPC

    U->>API: POST /certificates<br/>multipart file + X-Registrant
    API->>API: parseMediaUpload
    API->>UC: Execute(CertifyInput)

    UC->>UC: contentHash = SHA-256(content)
    UC->>DB: FindByHash(contentHash)

    alt já certificado
        DB-->>UC: certificado existente
        UC-->>API: ErrAlreadyCertified
        API-->>U: 409 Conflict
    end

    UC->>UC: phash = PHash256(content)

    opt é imagem
        UC->>CV: Compute(content)
        CV->>CV: decode, resize, ORB,<br/>encode JPEG normalizado
        CV-->>UC: FeatureSignature, jpegBytes
        UC->>S3: Put(contentHash + ".jpg", jpegBytes)
        S3-->>UC: OK
    end

    UC->>UC: commitment = FeatureCommitment(phash, signature)
    UC->>SC: eth_sendTransaction<br/>data = contentHash ‖ commitment
    SC-->>UC: txHash

    UC->>DB: BEGIN<br/>INSERT certificates<br/>INSERT phash_bands<br/>COMMIT
    DB-->>UC: id

    UC-->>API: CertifyOutput
    API-->>U: 201 Created
```

Pontos relevantes:

- A âncora on-chain é uma única transação com 64 bytes de calldata:
  `contentHash ‖ featureCommitment`. Não há ABI; o contrato lê a
  calldata bruta do bloco.
- Falha na extração ORB é logada mas não aborta a certificação. O anchor
  on-chain é preservado mesmo sem assinatura visual; nesses casos o
  `featureCommitment` vira o digest determinístico do bundle vazio.

## Verificação por arquivo (`POST /certificates/verify`)

```mermaid
sequenceDiagram
    actor U as Cliente
    participant API as HTTP Handler
    participant UC as VerifyUseCase
    participant CV as OpenCV Extractor
    participant DB as PostgreSQL
    participant S3 as S3 / MinIO

    U->>API: POST /certificates/verify<br/>multipart file
    API->>UC: Execute(VerifyInput{Content})

    UC->>UC: contentHash = SHA-256(content)
    UC->>DB: FindByHash(contentHash)

    alt match exato
        DB-->>UC: certificado
        UC-->>API: Certified=true
        API-->>U: 200 OK
    end

    UC->>UC: phashes = PHash256Variants(content)
    UC->>CV: Compute(content)
    CV-->>UC: candSig

    UC->>DB: FindCandidatesByPHashes(phashes, maxDist, topK=64)
    Note over DB: LSH prefilter via UNNEST(band_idx, band_value)<br/>JOIN phash_bands + Hamming-256 ≤ maxDist
    DB-->>UC: candidatos ordenados por distância

    loop cada candidato com Signature + ImageBlobKey
        UC->>S3: Get(ImageBlobKey)
        S3-->>UC: refImage
        UC->>CV: Match(refSig, candSig, refImage, candImage)
        CV-->>UC: MatchDecision
        alt Matched
            UC-->>API: Certified=true
            API-->>U: 200 OK
        end
    end

    UC-->>API: Certified=false
    API-->>U: 404 Not Found
```

## Verificação por hash (`GET /certificates/verify?hash=`)

```mermaid
sequenceDiagram
    actor U as Cliente
    participant API as HTTP Handler
    participant UC as VerifyUseCase
    participant DB as PostgreSQL

    U->>API: GET /certificates/verify?hash=<sha256-hex>
    alt hash vazio
        API-->>U: 400 Bad Request
    end
    API->>UC: Execute(VerifyInput{Hash})
    UC->>DB: FindByHash(hash)
    alt encontrado
        DB-->>UC: certificado
        UC-->>API: Certified=true
        API-->>U: 200 OK
    else não encontrado
        DB-->>UC: nil
        UC-->>API: Certified=false
        API-->>U: 404 Not Found
    end
```
