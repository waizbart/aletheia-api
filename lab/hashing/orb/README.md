# Lab: ORB + LAB residual para matching por conteúdo

Harness de pesquisa para validar um pipeline de matching perceptual que atenda os requisitos reais do Aletheia:

> Match quando o conteúdo da imagem original não foi alterado.
> Mismatch quando há alteração de conteúdo ou filtro que mude as cores do conteúdo.

Status atual: **25/25** nas amostras de `lab/hashing/testdata/`.

## Como rodar

```bash
cd lab/hashing/orb
go run .
```

Pré-requisitos: `libopencv-dev` + `pkg-config` instalados no sistema (CGO via `gocv.io/x/gocv v0.31.0`, pareado com OpenCV 4.6.x).

---

## Contexto e motivação

O lab original (`lab/hashing/{dHash,pHash,sha256}/`) testou três hashes agregados de 64 bits. O diagnóstico a partir desses runs foi:

| Transformação | dHash | pHash | aHash (prod, previsto) |
|---|---|---|---|
| qX 10–80, PNG, GIF, filtros leves | OK | OK | OK |
| rotação 90/180/270° | falha (19–33 bits) | falha (24–38) | falha |
| crop 10% | falha (16) | falha (18) | falha |
| `changed-2..6` | **falso positivo** (distância 0) | **falso positivo** (distância 0) | previsto falso positivo |

Dois problemas críticos:

1. **Invariância geométrica insuficiente.** Hashes agregados downsampleiam para 8×8 / 9×8 e destroem a informação que permite recuperar match sob rotação ou crop. Crop de 50%+ (requisito do usuário) é fisicamente impossível de compensar após essa redução.
2. **Sensibilidade a conteúdo baixa.** As imagens `changed-2..6` têm hashes idênticos ao original, apesar de pontos azuis visíveis adicionados em várias delas. Downsampling para 8 bits/pixel médio perde as edições localizadas.

A conclusão foi que **nenhum hash agregado serve** para esse requisito. Qualquer compressão que destrua a localização do sinal perceptual perde a capacidade de distinguir "crop de 50%" (muda apenas região visível) de "overlay de texto" (muda apenas região alterada) — ambos parecem "a maior parte do conteúdo igual".

A alternativa é pipeline baseado em **features locais** — pontos de interesse individuais casam partes da imagem que sobreviveram, e o resíduo (o que não casou) pode ser inspecionado pixel a pixel.

---

## Arquitetura do pipeline

Dado um par `(referência, candidato)`, o pipeline tem três estágios:

### 1. Alinhamento geométrico por ORB + RANSAC

- Extrai até **2000 keypoints ORB** de cada imagem em escala de cinza (`OrbFeatures = 2000`).
- Casa descritores via `BFMatcher` com norma Hamming.
- KNN k=2 + **Lowe's ratio test** em 0.75 (`LoweRatio = 0.75`) descarta matches ambíguos.
- `FindHomography` com **RANSAC** (`reprojThreshold=5.0`) resolve a transformação geométrica entre as duas imagens e retorna máscara de inliers.
- Exige `len(good) >= 8` e homografia válida.

O que esse estágio dá:
- Invariância a rotação arbitrária (ORB estima orientação por patch).
- Invariância a escala moderada, mudança de formato e qX (descritores binários ORB sobrevivem a compressão JPEG).
- Tolerância a crop severo (≥50%): RANSAC encontra homografia mesmo faltando metade dos pontos.

O que ele não dá:
- Rejeição de filtros de cor: ORB opera em luminância/gradiente, então filtros que mudam só hue/saturation preservam os keypoints. Precisamos do estágio 2 para isso.
- Rejeição de alterações pequenas de conteúdo: pontinhos azuis em áreas de baixa textura podem não gerar keypoint suficiente para cair na margem de inliers.

### 2. Warp do candidato para o espaço da referência

- Calcula `H^-1` via `gocv.Invert`.
- Aplica `WarpPerspective` do candidato em `H^-1`, produzindo uma imagem do mesmo tamanho da referência com os pixels do candidato já alinhados a ela.
- Em paralelo warpa uma máscara all-255 para saber que áreas da referência têm cobertura válida do candidato (necessário para crop, onde parte da referência não tem correspondência).

Warpar o candidato para a referência (em vez de comparar célula a célula via projeção direta) traz três ganhos:

- **Alinhamento pixel-a-pixel**, não célula-a-célula. Um pontinho que cai numa borda de célula projetada não é "diluído" entre células adjacentes.
- **Fácil lidar com crop**: a máscara de cobertura permite pular células que caem fora da área comum.
- **Grade uniforme** em coordenadas da referência: as células sempre têm o mesmo tamanho, o que simplifica thresholds comparáveis entre casos.

### 3. Residual LAB por célula

- Converte referência e candidato warped para espaço **LAB** (separa luminância de cromaticidade — a comparação fica naturalmente perceptual).
- Divide a referência em grade **128×128** (`GridSize = 128`; ~8×8 pixels por célula para uma imagem de 1024 px no lado maior).
- Para cada célula com cobertura ≥ 90% (`MinCoverage = 0.9`), calcula a distância euclidiana LAB entre a média da célula na referência e a média da mesma célula no candidato warped.
- Retorna três agregados:
  - `mean`: média das distâncias sobre células válidas.
  - `max`: maior distância de qualquer célula individual.
  - `cells`: quantas células contribuíram.

Por que duas métricas (mean e max) em vez de uma:

- `mean` captura **deslocamento global de cor** — um filtro que empurra todos os pixels em direção ao vermelho produz residual moderado em praticamente todas as células. Média alta, max não necessariamente extremo.
- `max` captura **alteração localizada** — um overlay de texto afeta só uma fração da imagem. Mean fica diluído para ~0, mas a célula que contém o overlay tem residual gigante.

As duas métricas são ortogonais. Um único threshold não consegue separar "filtro global" de "edição localizada"; precisa dos dois canais.

### 4. Decisão

```go
match  ⇔  inliers >= 20
        ∧  colorMean <= 12.0
        ∧  colorMax  <= 38.0
```

Thresholds calibrados empiricamente com as 25 amostras de `testdata/`.

---

## Escolhas e trade-offs

### Por que ORB e não SIFT/AKAZE/SuperPoint

| Detector | Qualidade | Licença | Custo | Status |
|---|---|---|---|---|
| SIFT | excelente | patente expirada, livre agora | mais lento | ok técnico |
| AKAZE | boa, escala invariante real | livre | mediano | ok técnico |
| ORB | boa para rotação + qX | livre | rápido | **escolhido** |
| SuperPoint / LoFTR | estado da arte | requer ONNX + pesos | muito alto | descartado |

Razão: ORB é rápido o suficiente para rodar em milhares de imagens num banco sem GPU, e sua qualidade já separa os casos de `testdata/` com margem. Se a margem ficar apertada em produção (ver seção "Limitações"), AKAZE é o próximo passo — mesma interface em `gocv`, zero custo de integração.

### Por que LAB e não RGB/HSV

- **LAB separa luminância (L) de cromaticidade (A, B)** — o olho humano percebe distâncias euclidianas em LAB como aproximadamente perceptualmente uniformes. Uma diferença de 5 em LAB é mais ou menos tão perceptível no verde quanto no azul.
- RGB: distâncias euclidianas não são perceptualmente uniformes. Uma distância RGB=20 num tom escuro é quase invisível, mas num tom claro salta aos olhos.
- HSV: hue é circular (0° ≈ 360°), o que quebra distância euclidiana direta.

LAB é o padrão clássico para métricas de distância de cor. `cv::cvtColor(..., COLOR_BGR2LAB)` do OpenCV é suficiente.

### Por que warp do candidato e não warp da referência

- Warpar **para** a referência mantém uma grade de células fixa nos dois casos → thresholds comparáveis entre amostras.
- Warpar a referência para o candidato exigiria recalcular a grade a cada comparação e introduziria variação de tamanho de célula.
- A coverage mask é trivial com warp do candidato: warpar uma imagem all-255 com o mesmo `H^-1`.

### Por que grade 128×128 (não 8×8 ou 16×16)

Iteração empírica:

| GridSize | `changed-2` max | Observação |
|---|---|---|
| 8 | — | cells muito grandes, perde mudanças locais pequenas por diluição |
| 16 | 1.14 | abaixo do threshold — falha |
| 32 | 4.42 | ainda abaixo |
| 64 | 14.87 | ainda abaixo |
| 128 | **39.66** | **pega** (margem de 1.66 sobre MaxCellDist=38) |

O tamanho da célula tem que ser ≤ ao tamanho da menor alteração esperada. Os pontinhos azuis em `changed-2` têm ~6-8 px; uma célula de 8 px por lado (128×128 grid em imagem 1024px) acomoda um ponto inteiro dentro de uma célula, maximizando o delta de média.

Trade-off: mais células = mais trabalho (mas O(pixels) é constante — só o número de iterações muda). Também aumenta o número de células sensíveis a ruído de interpolação do warp, o que empurra o max para cima em casos legítimos (ex.: rotação de 180° tem max=37.31 — apenas 0.69 abaixo do threshold). Se `OrbFeatures` ou `GridSize` crescerem mais, essa margem pode desaparecer.

### Por que mean AND max, não mean OR max

- Usando só `mean`: filtros globais são detectados, mas `changed-2` (mean=0.06) passa. Falha.
- Usando só `max`: alterações locais pegadas, mas filtros globais (mean alto, max relativamente baixo — `filter-2` max=28) escapam. Falha.
- AND: as duas condições precisam passar para ser match. Ambas as classes de defeito são cobertas.

### Por que redimensionar para 1024 px

- Custo do ORB e do warp escala com o número de pixels. Imagens grandes (4K+) seriam lentas.
- 1024 px é suficiente para ORB achar features robustas. Ganho marginal em detalhe não compensa o custo.
- Dados do projeto: `aletheia.png` tem 1.1 MB, provavelmente ~1500 px — cabe no limite.

### Por que coverage ≥ 90% (não 25%)

- Com 25%, células na borda de um crop ficavam meio-fora-meio-dentro da área coberta. A média LAB desse "meio-fora" contamina com o valor de borda (LAB = 0,128,128 preenchido pelo warp) e produz residual artificial.
- `aletheia-cropped-10p` com coverage=25% tinha max=108 (falsamente mismatch). Com coverage=90%, max=15.48 (match limpo).

---

## Tabela final de resultados

| Arquivo | Expected | Actual | Inliers | Mean LAB | Max LAB |
|---|---|---|---:|---:|---:|
| aletheia.jpg | match | match | 2000 | 0.00 | 0.00 |
| aletheia.png | match | match | 2000 | 0.00 | 0.00 |
| aletheia-q10.jpg | match | match | 951 | 5.79 | 18.55 |
| aletheia-q20.jpg | match | match | 1272 | 2.96 | 12.71 |
| aletheia-q30.jpg | match | match | 1450 | 2.05 | 13.99 |
| aletheia-q40.jpg | match | match | 1523 | 1.67 | 10.66 |
| aletheia-q50.jpg | match | match | 1578 | 1.41 | 11.49 |
| aletheia-q60.jpg | match | match | 1610 | 1.18 | 6.14 |
| aletheia-q70.jpg | match | match | 1643 | 1.00 | 9.49 |
| aletheia-q80.jpg | match | match | 1693 | 0.50 | 3.42 |
| aletheia-rotated-90.jpg | match | match | 1859 | 1.24 | 19.87 |
| aletheia-rotated-180.jpg | match | match | 1841 | 2.05 | **37.31** |
| aletheia-rotated-270.jpg | match | match | 1844 | 1.44 | 20.29 |
| aletheia-cropped-10p.jpg | match | match | 1232 | 0.81 | 15.48 |
| aletheia-changed-1.jpg | mismatch | mismatch | 1506 | 0.98 | 129.93 |
| aletheia-changed-2.jpg | mismatch | mismatch | 1974 | 0.06 | **39.66** |
| aletheia-changed-3.jpg | mismatch | mismatch | 1992 | 0.33 | 92.70 |
| aletheia-changed-4.jpg | mismatch | mismatch | 1542 | 0.82 | 102.17 |
| aletheia-changed-5.jpg | mismatch | mismatch | 1503 | 1.40 | 119.49 |
| aletheia-changed-6.jpg | mismatch | mismatch | 1311 | 2.17 | 181.17 |
| aletheia-changed-7.jpg | mismatch | mismatch | 1383 | 2.55 | 178.94 |
| aletheia-filter-1.jpg | mismatch | mismatch | 1200 | 21.05 | 33.96 |
| aletheia-filter-2.jpg | mismatch | mismatch | 1788 | 17.47 | 28.01 |
| aletheia-filter-3.jpg | mismatch | mismatch | 1160 | 19.48 | 49.49 |
| aletheia.gif | match | match | 1782 | 1.17 | 9.33 |

25/25 com `MinInliers=20`, `MaxColorDist=12.0`, `MaxCellDist=38.0`, `MinCoverage=0.9`.

### Bug de decodificação do GIF (corrigido)

Durante a calibração, `aletheia.gif` aparecia inicialmente com `mean=16.43, max=80.85`, sendo classificado como mismatch. A suspeita inicial era de que fosse conteúdo realmente diferente, mas inspeção detalhada dos deltas LAB por canal revelou um padrão anômalo: shift sistemático concentrado no canal B (amarelo↔azul), +15 na média global e +57 a +77 em regiões saturadas em tons quentes.

A causa raiz não estava no algoritmo nem na imagem: era o fallback de decodificação para formatos que o `IMRead` do OpenCV não suporta nativamente (GIF entra nessa categoria). O path era:

```go
image.Decode(f) → gocv.ImageToMatRGBA(img) → CvtColor(ColorRGBAToBGR)
```

Apesar do nome, `gocv.ImageToMatRGBA` escreve os bytes em ordem **BGRA** (seguindo a convenção interna do OpenCV). Aplicar `CvtColor(ColorRGBAToBGR)` em cima re-swappava R↔B erradamente, deixando bytes em ordem RGB dentro de um Mat que o resto do pipeline tratava como BGR. A conversão subsequente para LAB interpretava canais trocados, produzindo o shift observado.

O fix foi trocar `ColorRGBAToBGR` por `ColorBGRAToBGR` (que apenas dropa o canal alpha, preservando a ordem BGR correta). Após o fix, `aletheia.gif` casa com a JPG como esperado (mean=1.17, max=9.33). O JPG nunca foi afetado porque carrega via `IMRead` nativo, que entrega BGR correto diretamente.

Lição: qualquer formato que exija o fallback de decodificação (BMP com paleta, TIFF não-convencional, WebP dependendo do build do OpenCV, etc.) passaria pelo mesmo bug e falharia da mesma forma. Ao portar o pipeline para produção, padronizar o carregamento de imagens num único path verificado (ex.: sempre decodificar via stdlib Go + conversão manual para BGR com ordem de bytes explícita) evita essa classe de erro silencioso.

---

## Limitações e observações

### Margem apertada entre rotação e alteração sutil

- `aletheia-rotated-180` max = 37.31
- `aletheia-changed-2` max = 39.66
- Margem entre os dois: **2.35 pontos**, ou seja, o threshold `MaxCellDist=38` está a 0.69 de deixar `rotated-180` escapar como mismatch e a 1.66 de deixar `changed-2` escapar como match.

Essa margem apertada é provavelmente o ponto mais frágil do algoritmo. Origem do problema: warp via `H^-1` aplica interpolação bilinear, e rotações de múltiplos de 90° têm pixels com fase rotacionada onde a interpolação degrada sub-pixel. Isso eleva o resíduo em algumas células mesmo sendo uma transformação geométrica pura.

Mitigações possíveis (próximos passos):
- **Interpolação cúbica ou Lanczos** no warp em vez de bilinear — pode baixar o max de `rotated-180`.
- **Pré-alinhamento por ângulo múltiplo de 90°** (detectar rotação cardinal e aplicar `cv::rotate` antes do warp fino). Elimina artefato de interpolação em rotações cardinais.
- **Threshold adaptativo** baseado no número de inliers ou na qualidade do fit da homografia.

### Amostra pequena (25 imagens)

Os thresholds estão ajustados para passar exatamente nessas 25 amostras. Em produção com milhões de imagens, o ruído estatístico vai expor casos de borda que esse lab não cobre:
- Imagens com pouca textura (céu, paredes) → poucos keypoints → `MinInliers=20` pode não ser atingido mesmo em match legítimo.
- Alterações ainda menores que os pontinhos de `changed-2` (ex.: 1 pixel alterado) → não detectadas com `GridSize=128`.
- Filtros mais sutis que os três testados → podem ficar abaixo de `MaxColorDist=12`.

Antes de ir pra produção:
- Gerar dataset sintético maior (100–1000 imagens com variações controladas).
- Calibrar thresholds por busca em grade minimizando (falsos positivos + falsos negativos).
- Validar em dataset de "imagens da web" real com labels humanos.

### Custo computacional

Por par comparado, no hardware do lab (CPU):
- ORB detect+compute: ~50–100 ms por imagem.
- BFMatcher + RANSAC: ~20–50 ms.
- Warp + residual: ~30 ms.
- Total por comparação: ~200 ms. Aceitável para O(N) scan em DB com alguns milhares de certificados.

Não aceitável para milhões. Soluções para produção:
- **Pré-filtro por hash compacto** (ex.: pHash em 256 bits) para triagem dos candidatos top-K antes do matching por features.
- **Vector index** (pgvector / FAISS / hnswlib) sobre descritores agregados (ex.: média dos descritores ORB, ou embedding de visão).
- Cache do `FeatureSignature` já computado (descritores + LAB image), para evitar reprocessar.

### Dependência de CGO + OpenCV

- `gocv` exige `libopencv-dev` instalado. Dockerfile de produção precisa incluir.
- Cross-compilação entre OS fica restrita (CGO).
- Upgrades de OpenCV exigem sincronizar versão do `gocv` (v0.31.0 para 4.6; versões mais recentes quebram compilação em 4.6 — ver aruco.hpp compile errors).

### Espaço de assinatura

- Um certificado não é mais representado por 64 bits (aHash atual). A assinatura completa é:
  - ~2000 descritores ORB × 32 bytes = 64 KB
  - ~2000 keypoints × 16 bytes (x,y,angle,scale,response) = 32 KB
  - LAB image se guardada: 1024×1024×3 = 3 MB (não precisa persistir se recomputamos on demand)
- Schema novo necessário: `BIGINT` → tabela `certificate_feature_signatures` com `descriptors BYTEA`, `keypoints BYTEA`.
- On-chain: continua sendo só SHA-256 (parte de prova; descoberta é off-chain).

---

## Próximos passos

1. **Dataset sintético** com variações controladas (amplitude e localização de crop, intensidade de filtro, etc.) para curvar ROC e escolher thresholds robustamente.
2. **Portar para `internal/domain/feature_signature.go`** conforme plano principal. Adaptações:
   - Encapsular em `FeatureSignatureFromBytes(content []byte)`.
   - Separar extração (ORB+LAB) de comparação (`Match(a, b)`).
   - Substituir `log.Fatalf` por errors.
3. **Persistência.** Nova tabela + migration, com indexação inicial O(N) para scan linear.
4. **Pré-filtro.** Antes do scan, reduzir universo via pHash 256-bit ou embedding de baixa dimensão. Reaplicar o dual-signature só nos top-K.
5. **Dockerfile** com `libopencv-dev` no build stage e `libopencv-core`/imgproc/features2d/calib3d no runtime stage.
6. **Benchmark de performance** em corpus realístico antes de escolher se `gocv` é caminho final ou se vale investir em porta de ORB para pure Go (implausível) ou usar SuperPoint via `onnxruntime-go` (overkill atual, mas ponto no mapa).

## Alternativas descartadas (e por quê)

- **pHash / PDQ 256-bit** com 8 variantes (4 rotações × 2 espelhos): resolve rotação trivial, mas crop 50%+ continua destruindo o hash agregado. Incompatível com requisito de crop severo.
- **Block mean hash tileado**: ainda agrega. Crop 50% muda as tiles visíveis e destrói o match.
- **Embedding neural** (DINOv2/CLIP via ONNX): excelente qualidade perceptual e invariância geométrica, mas tipicamente **muito tolerante** a filtros de cor e alterações pequenas de conteúdo — contrário ao requisito explícito de rejeitar filtros que mudam cor. Além disso: +300 MB de modelo, CPU ~100–500 ms/imagem, dependência adicional. Mantida como Plano B se dual-signature não convergir em produção.
- **Implementação pure-Go de ORB**: não existe lib madura; escrever do zero seria um projeto por si só, com qualidade inferior ao OpenCV em meses de trabalho. Valor negativo.

---

## Arquivos

- `main.go` — harness completo (carregamento, ORB, BFMatcher, RANSAC, warp, residual, decisão, tabela).
- `go.mod` / `go.sum` — módulo isolado com `gocv.io/x/gocv v0.31.0`.
- `../testdata/` — 25 variações de `aletheia.jpg` compartilhadas com os labs pHash/dHash/sha256.
