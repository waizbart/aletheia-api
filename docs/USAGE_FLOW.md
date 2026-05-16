# Fluxo de Uso do Sistema

Visão orientada ao usuário. Para o detalhamento técnico, ver
[SEQUENCE_DIAGRAM.md](SEQUENCE_DIAGRAM.md) e
[ARCHITECTURE.md](ARCHITECTURE.md).

## Jornada do usuário

```mermaid
%%{init: {'theme':'base','themeVariables':{'background':'#ffffff','primaryColor':'#eef2f7','primaryBorderColor':'#1f2937','primaryTextColor':'#111827','lineColor':'#1f2937','textColor':'#111827','actorBkg':'#eef2f7','actorBorder':'#1f2937','actorTextColor':'#111827','actorLineColor':'#94a3b8','signalColor':'#1f2937','signalTextColor':'#111827','labelBoxBkgColor':'#fef3c7','labelBoxBorderColor':'#1f2937','labelTextColor':'#111827','loopTextColor':'#111827','noteBorderColor':'#b45309','noteBkgColor':'#fef3c7','noteTextColor':'#111827','activationBorderColor':'#1f2937','activationBkgColor':'#d1d5db','sequenceNumberColor':'#ffffff','edgeLabelBackground':'#ffffff'}}}%%
flowchart TD
    START(["Usuário tem uma imagem"]) --> Q1{Quer certificar<br/>ou verificar?}

    Q1 -- "Certificar<br/>(fonte confiável)" --> C1["POST /certificates"]
    C1 --> C2{API consegue<br/>processar?}
    C2 -- "Hash já existe" --> C_DUP["409 Conflict"]
    C2 -- "Erro de extração<br/>ou anchor" --> C_ERR["422 Unprocessable"]
    C2 -- "OK" --> C3["201 Created<br/>+ tx_hash + block_number"]

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
%%{init: {'theme':'base','themeVariables':{'background':'#ffffff','primaryColor':'#eef2f7','primaryBorderColor':'#1f2937','primaryTextColor':'#111827','lineColor':'#1f2937','textColor':'#111827','actorBkg':'#eef2f7','actorBorder':'#1f2937','actorTextColor':'#111827','actorLineColor':'#94a3b8','signalColor':'#1f2937','signalTextColor':'#111827','labelBoxBkgColor':'#fef3c7','labelBoxBorderColor':'#1f2937','labelTextColor':'#111827','loopTextColor':'#111827','noteBorderColor':'#b45309','noteBkgColor':'#fef3c7','noteTextColor':'#111827','activationBorderColor':'#1f2937','activationBkgColor':'#d1d5db','sequenceNumberColor':'#ffffff','edgeLabelBackground':'#ffffff'}}}%%
stateDiagram-v2
    [*] --> Submetida: POST /certificates

    Submetida --> Rejeitada: hash duplicado (409)
    Submetida --> Indexada: SHA-256 + pHash + ORB

    Indexada --> AncoradaPendente: eth_sendTransaction<br/>retorna txHash
    AncoradaPendente --> Persistida: INSERT certificates +<br/>phash_bands em transação

    Persistida --> [*]: 201 Created

    Persistida --> Consultada: GET/POST verify
    Consultada --> Persistida: resposta enviada

    note right of AncoradaPendente
      block_number ainda é 0;
      receipt não é consultado
      no fluxo atual.
    end note
```

## Modos de verificação

```mermaid
%%{init: {'theme':'base','themeVariables':{'background':'#ffffff','primaryColor':'#eef2f7','primaryBorderColor':'#1f2937','primaryTextColor':'#111827','lineColor':'#1f2937','textColor':'#111827','actorBkg':'#eef2f7','actorBorder':'#1f2937','actorTextColor':'#111827','actorLineColor':'#94a3b8','signalColor':'#1f2937','signalTextColor':'#111827','labelBoxBkgColor':'#fef3c7','labelBoxBorderColor':'#1f2937','labelTextColor':'#111827','loopTextColor':'#111827','noteBorderColor':'#b45309','noteBkgColor':'#fef3c7','noteTextColor':'#111827','activationBorderColor':'#1f2937','activationBkgColor':'#d1d5db','sequenceNumberColor':'#ffffff','edgeLabelBackground':'#ffffff'}}}%%
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
  `Match` ORB lendo a imagem de referência do S3.

## Notas operacionais

- A certificação é idempotente. O SHA-256 é a chave única, então
  reenviar o mesmo arquivo devolve sempre 409 e o cliente não precisa
  de controle de retry.
- O custo de verificação varia muito. `GET ?hash=` é barato.
  Verificação por arquivo de imagem grande pode ser dezenas de vezes
  mais cara por causa da extração ORB e do I/O em S3 por candidato.
- A API ainda não tem autenticação. O campo `Registrant` vem do header
  `X-Registrant` e é apenas registrado, não validado. Em produção isso
  precisa ser combinado com auth real antes de servir como prova de
  proveniência.
