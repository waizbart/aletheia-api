# Model Pipeline — Memory

## Visão Geral

Pipeline perceptual de 4 camadas para autenticidade de mídia.
Uma imagem candidata é comparada com uma imagem de referência (`aletheia.jpg`)
através de hashing perceptual e modelos de visão computacional (ONNX Runtime).

A pipeline decide se a imagem é **AUTÊNTICA** ou **ADULTERADA** com base em
similaridades calculadas em cada camada.

---

## Arquitetura

```
                    ┌─────────────┐
                    │   L1 pHash  │
                    │  (64 bits)  │
                    └──────┬──────┘
                           │
                    ≥ 98% ──┤── < 98%
                      │         │
               ┌──────▼──┐  ┌──▼─────────────┐
               │  PULA   │  │   L2 DINOv2    │
               │ L2 e L3 │  │  (384 bits)    │
               └──────┬──┘  └──┬──────────────┘
                      │        │
               ┌──────▼──┐  ┌──▼──────────────┐
               │   L4    │  │   L3 ConvNeXt   │
               │ Cores   │  │ (36864 bits)    │
               │(3072bit)│  └──┬──────────────┘
               └──────┬──┘    │
                      │       │
               ┌──────▼──┐  ┌─▼───────────────┐
               │ L4≥95%? │  │   L4 Cores      │
               └────┬────┘  │   (3072 bits)   │
                    │       └──┬──────────────┘
              sim ───┤── não   │
                    │         │
            ┌───────▼──┐  ┌──▼───────────────┐
            │ RÁPIDO   │  │ L2≥90% + L3≥90% │
            │ AUTÊNTICA│  │    + L4≥90%?     │
            └──────────┘  └──┬──────────────┘
                        sim ───┤── não
                              │         │
                     ┌────────▼──┐  ┌───▼──────────┐
                     │ COMPLETO  │  │  ADULTERADA  │
                     │ AUTÊNTICA │  └──────────────┘
                     └───────────┘
```

### Caminhos de decisão

- **Caminho rápido**: L1 ≥ 98% **E** L4 ≥ 95% → AUTÊNTICA
- **Caminho completo**: L2 ≥ 90% **E** L3 ≥ 90% **E** L4 ≥ 90% → AUTÊNTICA
- Caso contrário: ADULTERADA

Se L1 falha (< 98%), L2 e L3 são computados (caminho completo).
Se L1 passa mas L4 falha (< 95%), L2 e L3 são computados como fallback.

---

## Estrutura de arquivos

```
lab/model-pipeline/
├── Dockerfile              # Build em 2 estágios (golang:1.22-bookworm → debian:slim)
├── docker-compose.yml      # Orquestração Docker
├── export_models.py        # Exporta modelos PyTorch → ONNX
├── requirements.txt        # Dependências Python para export
├── go.mod / go.sum         # Módulo Go
├── main.go                 # Pipeline perceptual completo (~1100 linhas)
├── memory.md               # ← este arquivo
├── .dockerignore           # Exclui venv/ e outros do build context
├── models/
│   ├── dinov2_small.onnx          # DINOv2 ViT-S/14
│   └── convnextv2_base.onnx      # ConvNeXt V2-Base (sem GAP)
└── testdata/
    ├── aletheia.jpg               # Imagem de referência
    ├── aletheia-changed-*.jpg     # Imagens com modificações
    ├── aletheia-filter-*.jpg      # Imagens com filtros de cor
    ├── aletheia-q*.jpg            # Imagens com compressão JPEG
    ├── aletheia-rotated-*.jpg/png # Imagens rotacionadas
    ├── aletheia-cropped-10p.jpg   # Imagem com crop de 10%
    ├── aletheia-red-dress.png     # Vestido com cor alterada
    ├── aletheia-sword.png         # Espada adicionada à mão
    ├── aletheia.gif               # GIF (mesmo conteúdo do .jpg)
    └── aletheia.png               # PNG (mesmo conteúdo do .jpg)
```

---

## Camadas da Pipeline

### L1 — pHash (Perceptual Hash)

- **Biblioteca**: `github.com/corona10/goimagehash`
- **Hash**: 64 bits via `PerceptionHash(img)`
- **Similaridade**: `1.0 - distance/64`
- **Threshold**: ≥ 98% (distância ≤ 1)

Usado como triagem rápida. Se passa, pula as inferências ONNX (caras).

### L2 — DINOv2 ViT-S/14 (contexto global)

- **Modelo**: `facebook/dinov2-small` (HuggingFace)
- **Input**: 518×518 pixels (múltiplo de 14, patch size do ViT)
- **Pré-processamento**: Resize Lanczos + normalização ImageNet
- **Inferência**: ONNX Runtime via `onnxruntime_go`
- **Output**: `[1, 1370, 384]` (37×37 patches + 1 CLS token)
- **Feature**: Token CLS → vetor de 384 float32
- **Binarização**: threshold = média das 384 dimensões (da referência)
- **Hash**: 384 bits (48 bytes)
- **Threshold**: ≥ 90%

### L3 — ConvNeXt V2-Base (estrutura local) — SEM GAP

- **Modelo**: `convnextv2_base.fcmae_ft_in22k_in1k` (timm)
- **Input**: 1088×1088 pixels (≥1080p, múltiplo de 32)
- **Pré-processamento**: Resize Lanczos + normalização ImageNet
- **Inferência**: ONNX Runtime via `onnxruntime_go`
- **Saída do modelo**: `[1, 1024, 34, 34]` (mapa espacial, sem Global Average Pool)
- **Grid espacial**: 6×6 células sobre o mapa 34×34
- **Feature por célula**: média dos vetores 1024-dim dentro da célula
- **Thresholds**: 1024 thresholds (um por canal), calculados como a média
  do canal sobre todas as 36 células da referência
- **Hash**: 36 células × 1024 canais = **36.864 bits** (4.608 bytes)
- **Threshold**: ≥ 90%

**Por que sem GAP?** O GAP colapsa o mapa espacial 34×34 em um único vetor
1024-dim. Uma alteração localizada (ex: cabeça removida, ~80×80px) afeta
apenas ~6 de 1156 posições espaciais, diluindo o sinal. Com o grid 6×6,
as features são comparadas célula por célula, preservando a informação
espacial.

### L4 — Hash de Cores (grade 4×4, HSV + CIE Lab)

- **Implementação**: Go puro com `go-colorful` para conversões de cor
- **Redimensionamento**: Múltiplo de 4 mais próximo
- **Grade**: 4×4 = 16 regiões de tamanho igual
- **Por região**: 6 histogramas (H, S, V, L, a, b) de 32 bins cada
- **Total**: 16 × 6 × 32 = **3.072 floats**
- **Binarização**: thresholds por canal (6 thresholds H,S,V,L,a,b)
- **Hash**: 3.072 bits (384 bytes)
- **Threshold**: ≥ 90% (caminho completo) ou ≥ 95% (caminho rápido)

**Conversões de cor** (via `github.com/lucasb-eyer/go-colorful`):

- HSV: `c.Hsv()` — H [0,360), S [0,1], V [0,1]
- CIE Lab: `c.Lab()` — iluminante D65, L [0,100], a/b ~[-128,127]

---

## Constantes e Thresholds

| Constante           | Valor | Descrição                                   |
| ------------------- | ----- | ------------------------------------------- |
| `ThresholdL1Fast`   | 0.98  | pHash mínimo para caminho rápido            |
| `ThresholdL4Fast`   | 0.95  | L4 mínimo para caminho rápido               |
| `ThresholdL2Full`   | 0.90  | DINOv2 mínimo para caminho completo         |
| `ThresholdL3Full`   | 0.90  | ConvNeXt mínimo para caminho completo       |
| `ThresholdL4Full`   | 0.90  | Cores mínimo para caminho completo          |
| `DinoInputSize`     | 518   | Input DINOv2 (px), múltiplo de 14           |
| `ConvNextInputSize` | 1088  | Input ConvNeXt (px), ≥1080p, múltiplo de 32 |
| `ConvNextGridSize`  | 6     | Grade espacial do ConvNeXt                  |
| `ColorGridSize`     | 4     | Grade do hash de cores                      |
| `ColorHistBins`     | 32    | Bins por canal no hash de cores             |

---

## Resultados Esperados (testdata/)

Esperado vs obtido na versão atual do código:

| Imagem                   | Esperado  | Status        | Observação                            |
| ------------------------ | --------- | ------------- | ------------------------------------- |
| aletheia.gif             | match     | ✅ AUTÊNTICA  | Mesmo conteúdo, extensão diferente    |
| aletheia.png             | match     | ✅ AUTÊNTICA  | Mesmo conteúdo, extensão diferente    |
| aletheia-changed-1.jpg   | missmatch | ❌ AUTÊNTICA  | Cabeça removida (~80×80px)            |
| aletheia-changed-2.jpg   | match     | ✅ AUTÊNTICA  | Modificação sutil                     |
| aletheia-changed-3.jpg   | missmatch | ❌ AUTÊNTICA  |                                       |
| aletheia-changed-4.jpg   | missmatch | ❌ AUTÊNTICA  |                                       |
| aletheia-changed-5.jpg   | missmatch | ✅ ADULTERADA |                                       |
| aletheia-changed-6.jpg   | missmatch | ✅ ADULTERADA |                                       |
| aletheia-changed-7.jpg   | missmatch | ✅ ADULTERADA |                                       |
| aletheia-cropped-10p.jpg | match     | ✅ AUTÊNTICA  | Crop de 10% apenas                    |
| aletheia-filter-1.jpg    | missmatch | ✅ ADULTERADA | Cores alteradas                       |
| aletheia-filter-2.jpg    | missmatch | ✅ ADULTERADA |                                       |
| aletheia-filter-3.jpg    | missmatch | ✅ ADULTERADA |                                       |
| aletheia-q10.jpg         | match     | ❌ ADULTERADA | Compressão agressiva degrada features |
| aletheia-q20~80.jpg      | match     | ✅ AUTÊNTICA  | Compressão moderada a leve            |
| aletheia-red-dress.png   | missmatch | ✅ ADULTERADA | Cor do vestido alterada               |
| aletheia-rotated-\*      | match     | ❌ ADULTERADA | Pipeline ainda não normaliza rotação  |
| aletheia-sword.png       | missmatch | ✅ ADULTERADA | Objeto adicionado                     |

---

## Dependências

### Go (diretas)

```
github.com/corona10/goimagehash       # pHash (L1)
github.com/disintegration/imaging     # Manipulação de imagens
github.com/lucasb-eyer/go-colorful    # Conversões de cor HSV/Lab (L4)
github.com/yalue/onnxruntime_go       # ONNX Runtime bindings (L2, L3)
```

### Python (export_models.py)

```
torch >= 2.0.0
transformers >= 4.30.0
timm >= 0.9.0
onnxscript
onnx
```

### Sistema

- ONNX Runtime C library (`libonnxruntime.so`) — instalado via Dockerfile
- CGO_ENABLED=1 — necessário para `onnxruntime_go`

---

## Como usar

### 1. Exportar modelos ONNX

```bash
cd lab/model-pipeline
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
python export_models.py
```

### 2. Build e execução (Docker)

```bash
docker compose build
docker compose run --rm pipeline
```

### 3. Execução local (Go, requer ONNX Runtime instalado)

```bash
go mod tidy
CGO_ENABLED=1 go run main.go --verbose
```

### Flags disponíveis

| Flag         | Padrão                      | Descrição                      |
| ------------ | --------------------------- | ------------------------------ |
| `--verbose`  | false                       | Saída detalhada                |
| `--ref`      | testdata/aletheia.jpg       | Imagem de referência           |
| `--testdir`  | testdata                    | Diretório com imagens de teste |
| `--dino`     | models/dinov2_small.onnx    | Modelo ONNX DINOv2             |
| `--convnext` | models/convnextv2_base.onnx | Modelo ONNX ConvNeXt           |

---

## Diagrama de Dados

```
               ┌─────────────┐
               │  aletheia   │
               │   .jpg      │
               └──────┬──────┘
                      │
                      ▼
            ┌─────────────────────┐
            │  Compute Reference  │
            │  Hashes             │
            └──┬──────┬──────┬────┘
               │      │      │
        ┌──────▼┐ ┌──▼──┐ ┌─▼──────┐
        │ pHash │ │DINO │ │ConvNeXt│
        │64 bits│ │384b │ │36864b  │
        └───────┘ └─────┘ └────────┘
                              │
               ┌──────────────▼──────────────┐
               │     Color Hash (L4)         │
               │  16 regiões × 6 canais      │
               │  × 32 bins = 3072 floats    │
               │  → binarizar → 3072 bits    │
               └─────────────────────────────┘

   Para cada imagem candidata:

   1. pHash → comparar com referência
   2. Se ≥ 98%: L4 direto (pula L2/L3)
   3. Caso contrário: DINOv2 + ConvNeXt + L4
   4. Decisão final
```

---

## Notas Técnicas

- **ONNX Runtime**: a biblioteca `onnxruntime_go` procura por `libonnxruntime.so`.
  O Dockerfile baixa a versão 1.20.0 e instala em `/usr/local/lib/`.
- **Dados externos**: modelos grandes (>2GB) usam formato external data (`.onnx.data`).
  O `export_models.py` os embute de volta no `.onnx` via `onnx.load/save`.
- **Binarização**: o threshold é calculado **exclusivamente da imagem de referência**
  e reutilizado para todas as candidatas. Isso garante consistência na distância
  de Hamming entre diferentes imagens.
- **CGO**: obrigatório para `onnxruntime_go`. O Dockerfile configura
  `CGO_CFLAGS` e `CGO_LDFLAGS` para incluir os headers e libs do ONNX Runtime.

### Problemas conhecidos

1. **Rotação**: imagens rotacionadas (90°, 180°, 270°) falham todas as camadas.
   A pipeline não implementa normalização de rotação.
2. **changed-1**: falsa autêntica — DINOv2 não detecta a cabeça removida como
   alteração relevante nas features globais. O ConvNeXt espacial deve melhorar
   após re-export com o modelo sem GAP.
3. **q10**: falsa adulterada — compressão JPEG agressiva degrada as features do
   ConvNeXt/DINOv2 abaixo do threshold de 90%.
