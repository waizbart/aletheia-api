# Fluxo de Uso do Sistema

Visão orientada ao usuário. Para o detalhamento técnico, ver
[SEQUENCE_DIAGRAM.md](SEQUENCE_DIAGRAM.md) e
[ARCHITECTURE.md](ARCHITECTURE.md).

## Jornada do usuário

```mermaid
flowchart TD
    START(["Alguém tem uma imagem"]) --> Q1{Quer certificar<br/>ou verificar?}

    Q1 -- "Certificar" --> C0{Dispositivo já<br/>inscrito?}
    C0 -- "Não" --> C_ENR["POST /captures/nonce<br/>POST /devices<br/>(atestação de hardware)"]
    C_ENR --> C_ENR_R{Atestação passa<br/>na política?}
    C_ENR_R -- "Não" --> C_403["403 Forbidden<br/>com o portão que falhou"]
    C_ENR_R -- "Sim" --> C0
    C0 -- "Sim" --> C1["POST /captures/nonce<br/>assinar no TEE<br/>POST /captures"]
    C1 --> C2{API consegue<br/>processar?}
    C2 -- "Cota do plano esgotada" --> C_402["402 Payment Required"]
    C2 -- "Assinatura não confere" --> C_SIG["403 Forbidden"]
    C2 -- "Desafio já usado" --> C_409["409 Conflict"]
    C2 -- "Hash já certificado" --> C_DUP["409 Conflict"]
    C2 -- "OK" --> C3["201 Created<br/>(ancoragem vem depois)"]

    Q1 -- "Verificar" --> V0{Tenho a imagem<br/>ou só o hash?}
    V0 -- "Só o hash" --> V_H["GET /certificates/verify?hash="]
    V_H --> V_H_R{Hash existe<br/>no banco?}
    V_H_R -- "Sim" --> V_OK["200 OK<br/>com proveniência"]
    V_H_R -- "Não" --> V_NO["404 Not Found"]

    V0 -- "Tenho a imagem" --> V_F["POST /certificates/verify"]
    V_F --> V_F1{SHA-256 exato<br/>existe?}
    V_F1 -- "Sim" --> V_OK
    V_F1 -- "Não" --> V_F3["LSH bands,<br/>Hamming-256,<br/>match ORB nos<br/>top-K candidatos"]
    V_F3 --> V_F4{Algum candidato<br/>casou visualmente?}
    V_F4 -- "Sim" --> V_OK_SIM["200 OK<br/>por similaridade visual"]
    V_F4 -- "Não" --> V_NO
```

## Estados de um certificado

```mermaid
stateDiagram-v2
    [*] --> Submetida: POST /captures

    Submetida --> Rejeitada: desafio inválido (409)<br/>assinatura não confere (403)<br/>dispositivo revogado (403)
    Submetida --> Indexada: SHA-256 + pHash + ORB<br/>+ grade de cores

    Indexada --> Persistida: INSERT certificates +<br/>phash_bands em transação
    Persistida --> [*]: 201 Created

    Persistida --> AguardandoAncora: já verificável,<br/>ainda sem prova on-chain
    AguardandoAncora --> Ancorada: worker commita o lote<br/>sob uma raiz Merkle

    Ancorada --> Consultada: GET/POST verify
    Consultada --> Ancorada: resposta enviada

    note right of AguardandoAncora
      O certificado é válido e
      verificável imediatamente.
      A âncora acrescenta uma prova
      pública de existência, num
      lote, no próximo ciclo.
    end note

    note right of Ancorada
      block_number vem do receipt,
      então é sempre um bloco real.
      merkle_proof permite conferir
      contra a raiz on-chain sem
      confiar nesta API.
    end note
```

## Modos de verificação

```mermaid
flowchart LR
    IN["Entrada do<br/>verificador"] --> MODE{Tipo de<br/>verificação}

    MODE -- "hash" --> M1["Lookup direto em<br/>certificates.content_hash"]
    MODE -- "arquivo idêntico" --> M2["SHA-256 →<br/>lookup exato"]
    MODE -- "arquivo modificado<br/>(re-encode, crop leve,<br/>rotação, flip)" --> M3["pHash variants →<br/>LSH bands →<br/>Hamming-256 →<br/>ORB match"]

    M1 --> OUT{Match?}
    M2 --> OUT
    M3 --> OUT

    OUT -- sim --> R1["Certificate<br/>+ tx_hash + registrant"]
    OUT -- não --> R2["certified=false"]
```

Diferença prática entre os modos:

- Por hash e por arquivo idêntico são lookups O(1) num índice único.
- Por arquivo modificado é o caminho caro. Roda OpenCV no candidato
  (ORB + JPEG normalizado), faz prefilter LSH em `phash_bands`,
  calcula Hamming-256 contra cada sobrevivente e finalmente roda
  `Match` ORB + resíduo de cor contra a grade LAB armazenada no
  certificado.

## Notas operacionais

- A certificação é idempotente. O SHA-256 é a chave única, então
  reenviar o mesmo arquivo devolve sempre 409 e o cliente não precisa
  de controle de retry.
- O custo de verificação varia muito. `GET ?hash=` é barato.
  Verificação por arquivo de imagem grande pode ser dezenas de vezes
  mais cara por causa da extração ORB e do match por candidato.
- A API ainda não tem autenticação. O campo `Registrant` vem do header
  `X-Registrant` e é apenas registrado, não validado. Em produção isso
  precisa ser combinado com auth real antes de servir como prova de
  proveniência.
