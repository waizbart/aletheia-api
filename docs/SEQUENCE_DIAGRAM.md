```mermaid
sequenceDiagram
    actor U as 👤 Usuário
    participant SDK as 📱 SDK / Dispositivo<br/>(Secure Enclave + C2PA)
    participant API as ⚙️ API Go
    participant DB as 🗄️ PostgreSQL
    participant SC as ⛓️ Smart Contract<br/>(EVM)

    rect rgb(220, 240, 255)
        note over U, SDK: CAPTURA
        U->>SDK: Captura mídia (foto/vídeo)
        SDK->>SDK: Computa SHA-256 + pHash
        SDK->>SDK: Assina hash com chave privada<br/>(Secure Enclave)
        SDK->>SDK: Gera manifesto C2PA<br/>(hash + assinatura + metadados<br/>timestamp, dispositivo, localização)
    end

    rect rgb(220, 255, 220)
        note over SDK, SC: REGISTRO
        SDK->>API: POST /certificates<br/>(mídia + manifesto C2PA + assinatura)
        API->>API: Valida tipo de mídia
        API->>API: Recomputa SHA-256 + pHash
        API->>API: Compara hash recebido<br/>com hash recomputado
        alt Hash divergente
            API-->>SDK: 400 Bad Request<br/>(mídia adulterada em trânsito)
        end
        API->>API: Verifica assinatura C2PA<br/>contra chave pública do dispositivo
        alt Assinatura inválida
            API-->>SDK: 401 Unauthorized<br/>(assinatura inválida)
        end
        API->>DB: Verifica se hash já existe
        alt Já certificado
            DB-->>API: Certificado existente
            API-->>SDK: 409 Conflict<br/>(conteúdo já certificado)
        end
        API->>SC: Publica SHA-256<br/>register(contentHash, registrant)
        SC-->>API: Emite evento ContentRegistered<br/>(hash + registrant + blockTimestamp)
        API->>DB: Persiste certificado<br/>(hash, pHash, tx_hash,<br/>block_number, registrant)
        DB-->>API: OK
        API-->>SDK: 201 Created<br/>(certificado com tx_hash<br/>e block_number)
        SDK-->>U: ✅ Mídia certificada
    end

    rect rgb(255, 245, 220)
        note over U, SC: VERIFICAÇÃO POR ARQUIVO
        U->>API: POST /certificates/verify<br/>(arquivo)
        API->>API: Recomputa SHA-256 + pHash
        API->>DB: Busca por SHA-256 exato
        alt Encontrado
            DB-->>API: Certificado
            API-->>U: 200 OK — certificado + evidência<br/>de proveniência
        else Não encontrado — imagem
            API->>DB: Busca por pHash<br/>(distância de Hamming ≤ 8)
            alt Similar encontrado
                DB-->>API: Certificado por similaridade
                API-->>U: 200 OK — certificado por similaridade visual
            else Não encontrado
                API-->>U: 404 Not Found<br/>(conteúdo não certificado)
            end
        end
    end

    rect rgb(255, 220, 220)
        note over U, SC: VERIFICAÇÃO POR HASH
        U->>API: GET /certificates/verify?hash=
        API->>DB: Busca SHA-256 no banco
        alt Encontrado
            DB-->>API: Certificado
            API-->>U: 200 OK — certificado + evidência<br/>de proveniência
        else Não encontrado
            API-->>U: 404 Not Found
        end
    end
```
