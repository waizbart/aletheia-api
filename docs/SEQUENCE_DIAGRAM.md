# Diagramas de Sequência

Fluxos atuais da Aletheia API, conforme implementados em
`internal/usecase` e suas portas (`internal/usecase/ports.go`).

## Captura atestada (`POST /captures`)

```mermaid
sequenceDiagram
    actor SDK as SDK / app de câmera
    participant API as HTTP Handler
    participant AUTH as APIKeyAuth
    participant Q as UsageUseCase
    participant UC as AttestedCaptureUseCase
    participant CERT as CertifyUseCase
    participant CV as OpenCV Extractor
    participant DB as PostgreSQL

    SDK->>API: POST /captures/nonce
    API->>DB: INSERT capture_nonces
    API-->>SDK: nonce (uso único, curta validade)

    SDK->>SDK: assina no elemento seguro<br/>SHA-256(bytes) ‖ nonce ‖ metadados

    SDK->>API: POST /captures<br/>file + device_id + nonce<br/>+ signature + metadados
    API->>AUTH: Bearer alk_…
    AUTH->>DB: FindOrgByAPIKeyHash
    AUTH-->>API: org

    API->>Q: Check(org, attested_capture)
    alt cota esgotada
        Q-->>API: ErrQuotaExceeded
        API-->>SDK: 402 Payment Required
    end

    API->>UC: Execute(AttestedCaptureInput)

    UC->>DB: Consume(nonce)<br/>UPDATE … WHERE consumed_at IS NULL<br/>AND expires_at > now
    alt desconhecido, expirado ou já usado
        DB-->>UC: 0 linhas
        UC-->>API: ErrNonceUnusable
        API-->>SDK: 409 Conflict
    end

    UC->>DB: FindByID(device_id)
    alt revogado ou de outro tenant
        UC-->>API: ErrDeviceRevoked / ErrDeviceNotFound
        API-->>SDK: 403 / 404
    end

    UC->>UC: contentHash = SHA-256(bytes)
    UC->>UC: VerifyCaptureSignature<br/>(chave fixada na inscrição)
    alt bytes ou metadados alterados
        UC-->>API: ErrCaptureSignature
        API-->>SDK: 403 Forbidden
    end

    UC->>CERT: Execute(CertifyInput + proveniência)
    CERT->>DB: FindByHash(contentHash)
    CERT->>CERT: phash = PHash256(content)
    opt é imagem
        CERT->>CV: Compute(content)
        CV-->>CERT: FeatureSignature<br/>(descritores + keypoints + grade LAB)
    end
    CERT->>CERT: commitment = FeatureCommitment(phash, signature)
    CERT->>DB: BEGIN<br/>INSERT certificates<br/>INSERT phash_bands<br/>COMMIT

    CERT-->>UC: CertifyOutput
    UC-->>API: CertifyOutput
    API->>Q: Record(org, attested_capture)
    API-->>SDK: 201 Created
```

Pontos relevantes:

- **O nonce é consumido antes da assinatura ser conferida.** Uma assinatura
  ruim ainda queima o desafio, senão um atacante manteria um desafio aberto e
  moeria assinaturas contra ele.
- **A cota é conferida antes e contabilizada depois.** Uma captura recusada
  nunca chega numa fatura. Perder a contagem não derruba a captura: uma
  subcontagem é problema de cobrança, uma captura falha é problema do cliente.
- **A certificação não fala com a blockchain.** O certificado já é válido e
  verificável; a ancoragem vem depois, em lote.
- Falha na extração ORB é logada mas não aborta a certificação; nesses casos o
  `featureCommitment` vira o digest determinístico do bundle vazio.

## Ancoragem em lote (worker)

```mermaid
sequenceDiagram
    participant W as AnchorUseCase
    participant DB as PostgreSQL
    participant SVC as EVMAnchorService
    participant NODE as EVM JSON-RPC

    loop a cada ANCHOR_INTERVAL
        W->>DB: PendingLeaves(limit)<br/>WHERE anchor_id IS NULL
        alt nada pendente
            DB-->>W: []
            Note over W: não gasta transação
        else há certificados
            DB-->>W: certificados
            W->>W: folhas = keccak(keccak(hash ‖ commitment))
            W->>W: BuildMerkleTree → raiz + provas

            W->>SVC: RegisterRoot(raiz, n)
            SVC->>NODE: eth_getTransactionCount / eth_gasPrice
            SVC->>SVC: assina EIP-155 (RLP + secp256k1)
            SVC->>NODE: eth_sendRawTransaction
            NODE-->>SVC: txHash
            loop até confirmar ou expirar
                SVC->>NODE: eth_getTransactionReceipt
            end
            NODE-->>SVC: blockNumber real

            alt transação não confirmou
                SVC-->>W: erro
                Note over W: lote segue pendente;<br/>o próximo ciclo tenta de novo
            else confirmada
                SVC-->>W: txHash, blockNumber
                W->>DB: BEGIN<br/>INSERT anchors<br/>UPDATE certificates (prova + índice)<br/>COMMIT
            end
        end
    end
```

## Verificação por arquivo (`POST /certificates/verify`)

```mermaid
sequenceDiagram
    actor U as Cliente
    participant API as HTTP Handler
    participant UC as VerifyUseCase
    participant CV as OpenCV Extractor
    participant DB as PostgreSQL

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

    loop cada candidato com Signature + ColorGrid
        UC->>CV: Match(refSig, candSig, candImage)
        Note over CV: geometria ORB+RANSAC dos descritores<br/>resíduo de cor vs grade LAB armazenada
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
