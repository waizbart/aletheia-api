# Execução do benchmark final — registro de procedência

Este arquivo registra **como** os números do capítulo 7 foram produzidos, para que
qualquer valor do texto possa ser rastreado até um arquivo e um commit. Os
resultados em si estão nos arquivos listados na tabela de procedência.

## Congelamento

| item | valor |
| --- | --- |
| Tag | `tcc-benchmark-v1` |
| Commit | `361c65d8001c996aa76f70c732bcf6afa1797718` |
| Branch | `tcc/benchmark-v1` |
| Ambiente completo | `results/ambiente.txt` |

Os limiares congelados, conferidos contra o `thresholds_snapshot` do manifest
pelo próprio teste, que se recusa a rodar se divergirem:

| limiar | valor |
| --- | --- |
| `MinInliers` | 20 |
| `MaxColorMean` | 8,0 |
| `MaxCellDist` | 38,0 |
| `MinAreaCoverage` | 0,42 |
| `MaxPHashDistance` | 96 |
| `verifyTopK` | 64 |

Nenhum limiar foi alterado em nenhum momento. As duas mudanças de código que
entraram antes do congelamento estão descritas abaixo.

### Commits depois da tag

Um commit entrou depois do congelamento, `5a36615`. Ele **não** toca o sistema
sob teste: corrige o `TestDataset_FullMatrix` (a verificação cruzada opcional do
passo 2, que abortava na primeira base sem features ORB) e o
`scripts/tcc_summary.py` (que contava como erro de atribuição os 993
`different_image` que o SHA-256 resolve corretamente). O código do pipeline
medido continua sendo o da tag.

## Desvios em relação ao roteiro

Quatro desvios, todos por restrição do ambiente ou por economia de execução.
Nenhum altera o sistema sob teste.

### 1. Execução em container, não no host

O host não tem Go, `gcc`, `pkg-config` nem OpenCV, e o `sudo` exige senha, então
não foi possível instalar a toolchain. Tudo roda num container construído a
partir do `Dockerfile` do próprio repositório (`golang:1.25-bookworm` +
`libopencv-dev`), que fornece Go 1.25.14 e OpenCV 4.6.0 — exatamente as versões
que o roteiro pede.

O repositório é montado **no mesmo caminho absoluto do host**
(`/home/waizbart/projects/aletheia-api`), porque o manifest guarda caminhos
absolutos e o `TestDataset_FullMatrix` os lê sem remapear.

### 2. Quatro núcleos, não oito

A máquina é um servidor virtual AMD EPYC-Rome com **4 vCPU** e 7,6 GiB de RAM.
Os tempos do roteiro, extrapolados para 8 núcleos, praticamente dobram. Como é
servidor e não notebook, não houve risco de suspensão e o `systemd-inhibit` foi
dispensado.

A rodada de latência usou `TCC_WORKERS=1`, conforme o roteiro, então a medição
de tempo não sofre disputa de CPU. Os três níveis foram encadeados em sequência,
nunca em paralelo, pela mesma razão.

### 3. Manifest regerado; variantes reaproveitadas

O dataset de 993 bases já existia em disco de uma execução anterior, mas o
`manifest.json` estava defasado em três pontos: caminhos absolutos de outro
prefixo de montagem (`/work/...`), `thresholds_snapshot` sem
`min_area_coverage` (gerado antes de a constante existir) e dois rótulos
divergentes do registry atual — `crop_border_20pct` estava como
`expected_match: true` e hoje é `false`, e `format_change_gif` estava como
`image/gif` e hoje é `image/png`.

O manifest foi regerado com o código congelado, reaproveitando as imagens em
cache. As variantes **não** foram regeradas, e isso é deliberado:
`internal/dataset/transform/builders.go` usa um gerador aleatório global
compartilhado entre os workers, então as famílias `noise_light` e
`localized_recolor` não são reproduzíveis entre execuções paralelas. Reaproveitar
os arquivos existentes preserva a reprodutibilidade; regerá-los a destruiria.
As duas únicas transformações afetadas pela regeração foram as que mudaram de
rótulo ou de formato.

O catálogo do Picsum tem **993** imagens com ID numérico, não 1.000, então
`--count 1000` seleciona todas as 993. Reporte 993 bases e 53.622 amostras.

### 4. Passo 4 sem `docker compose down -v`

O roteiro manda apagar o volume do Postgres para aplicar as 8 migrações do zero.
Isso foi dispensado após verificar que o banco em execução já tem as 8 tabelas e
todas as 19 colunas que o código lê em `certificates`, mais `phash_bands`
completa. O teste e2e executa `TRUNCATE certificates CASCADE` por conta própria,
que é todo o estado limpo de que ele precisa.

A razão para dispensar: um `docker compose override.yml` local aponta o serviço
`api` para a Polygon mainnet com uma chave de ancoragem real, e o `down -v`
reiniciaria esse container e destruiria um volume de minio sem relação com o
experimento. O banco continha 1 certificado residual, truncado pelo teste.

## Mudanças de código antes do congelamento

### Correção: decodificadores de BMP, TIFF e WebP no pHash

A API aceita todo formato que o extractor baseado em OpenCV decodifica, o que
inclui BMP, TIFF e WebP. O pHash seguia outro caminho: usava `image.Decode`, que
só conhece os formatos cujos pacotes estão registrados. Essas três entradas
produziam pHash nulo, e a verificação respondia "imagem não decodificável" para
imagens que o matcher trata sem problema — por isso `format_change_bmp` e
`format_change_tiff` pontuavam 0% no pipeline e 100% par a par.

Os decodificadores de `golang.org/x/image` foram registrados. É correção de bug,
não ajuste de limiar, e está declarada aqui para o texto. A versão fixada é
`v0.34.0`, porque a `@latest` exige Go ≥ 1.26.

### Caracterização: o resíduo de cor das rotações cardeais

O roteiro registrava suspeita de erro no warp, porque 90° e 180° passavam a 35%
e 270° a 90% na rodada de 20 bases. A assimetria foi reproduzida (50% / 25% /
83% em 12 bases) e a causa isolada:

- O gate que falha é **sempre** `ColorMax > MaxCellDist`. Nunca inliers (~1.700),
  nunca cobertura (~0,99), nunca o pré-filtro pHash (Hamming 0–8).
- Substituindo a homografia estimada por RANSAC pela **rotação analítica
  exata**, `ColorMax` cai de 23–86 para 2,0–6,3 e todas as rotações passam.

Logo o gate de cor está correto e o limite é a **precisão do alinhamento
geométrico**: o grid de 128×128 sobre uma referência de 800×600 dá células de
cerca de 6×5 px, onde um erro sub-pixel desloca a média de uma célula de alta
frequência em dezenas de unidades LAB, e o gate por célula lê isso como uma
edição localizada.

Duas correções foram medidas e **rejeitadas**:

| estratégia | resultado |
| --- | --- |
| Reajustar a homografia nos inliers do RANSAC | indistinguível do baseline em 314 pares; o OpenCV já refina no conjunto de consenso |
| Apertar o limiar do RANSAC de 5,0 para 2,0 | rotações melhoram (90°: 40→67%, 180°: 20→73%), escala e corte pioram (downscale 33→20%, upscale 80→53%); 249 → 252 acertos de 314 |

Nenhuma linha de negativo mudou em nenhuma das estratégias, ou seja, a precisão
é insensível a essa escolha. Como nenhuma das duas é ganho líquido e qualquer
outra alternativa exigiria mexer numa constante congelada ou ter a imagem de
referência — que a arquitetura deliberadamente não armazena, guardando apenas o
grid LAB de 128×128 —, o matcher **não** foi alterado. O diagnóstico está fixado
em `TestCardinalRotation_ResidualIsAlignmentBound`.

## Bases de calibração do estrato limítrofe

Os comentários do registry citam taxas observadas em 100 imagens do Picsum, ou
seja, o estrato "limítrofe" foi definido depois de ver o sistema. Essas 100 bases
estão dentro das 993, então as métricas também devem ser reportadas sem elas.

Os IDs foram **reconstruídos**, não recuperados de um registro daquela execução.
`cmd/datasetgen` usa `--count 100 --seed 42` por padrão para a fonte picsum, e
`source.Picsum.List` embaralha o catálogo travado em `cache/source.lock.json` com
essa seed antes de cortar. O embaralhamento é estável no prefixo, então as 100
primeiras bases da seleção com `--count 1000` são exatamente as da seleção com
`--count 100`. Verificado: a lista coincide com os 100 primeiros
`base_image_id` distintos na ordem do `manifest.json`.

Receita reprodutível, sem precisar de Go: **os 100 primeiros `base_image_id`
distintos, na ordem em que aparecem no `manifest.json`**. O resultado está em
`results/calibration_base_ids.txt`.

Como é reconstrução por inferência e não um registro, declare isso no texto.

Métricas sem essas bases:

```bash
python3 scripts/tcc_summary.py results/tcc_results.csv \
  --exclude results/calibration_base_ids.txt \
  --out results/resumo_sem_calibracao.md
```

## Controle negativo adicionado

O manifest não consegue expressar o controle que mais importa para um
certificador. O `different_image` devolve os bytes do par intactos, e o par é
ele mesmo certificado, então o SHA-256 resolve antes de qualquer comparação
visual — ele nunca exercita o caminho que decidiria uma falsificação.

O `uncertified_image` consulta os bytes de uma base contra uma *view* do
repositório com o certificado dela escondido. O SHA-256 então não resolve, e a
correspondência visual roda contra os outros 992 certificados sem resposta
correta disponível. É um controle por base, 993 no total.

A *view* esconde o certificado em vez de removê-lo do banco, para o controle
rodar em paralelo sem corrida com as outras amostras.

## Fidelidade da simulação

O `TestTCC_PipelineSimulation` chama o `usecase.VerifyUseCase.Execute` **real**,
alcançando-o pela porta `observability.Recorder`, em vez de reimplementar o
pipeline. Os limites de estágio e a lógica de decisão são os de produção, e os
tempos por estágio vêm da própria instrumentação.

Duas diferenças conhecidas em relação à API real:

1. O banco é em memória. A lógica de `FindCandidatesByPHashes` é reproduzida
   fielmente — sonda por bandas, re-pontuação com Hamming exato de 256 bits,
   corte em `MaxPHashDistance`, ordenação por distância, janela top-K —, mas a
   ordem entre candidatos empatados em distância difere: o Postgres a deixa
   indefinida, e a simulação desempata pelo content hash para ser reprodutível.
   O passo 4 é o que estabelece se isso importa.
2. `verifyTopK` não é exportado. A cópia no teste é conferida em **cada amostra**
   contra a contagem de candidatos que o use case reportou, então ela não pode
   derivar em silêncio.

## Achado que muda o relato: duas bases são a mesma fotografia

A rodada de acurácia acusou 27 amostras atribuídas a "outro certificado" por
correspondência visual e 2 acertos no controle `uncertified_image`. Investigados,
os dois números têm a mesma causa única, e não é erro do sistema.

Todas as 27 vêm de uma só base, `picsum_456`. E `picsum_456` e `picsum_128` são
**a mesma fotografia servida sob dois IDs do Picsum**:

| medida | valor |
| --- | --- |
| Distância pHash entre as duas bases | **0** |
| Inliers do matcher entre elas | 2000 (o teto de `OrbFeatures`) |
| `ColorMean` / `ColorMax` | 0,44 / 0,9 |
| Cobertura | 1,000 |
| Pares de bases dentro de Hamming 32, em 492.528 pares | **1** (exatamente este) |

Não há duplicatas byte-idênticas entre as 993 bases; esta é quase-duplicata de
conteúdo, com encodings distintos.

Consequências para o texto:

- As 27 atribuições a "outro certificado" são variantes da `picsum_456` casando
  com o certificado da gêmea idêntica. É a resposta **correta**: as imagens são o
  mesmo conteúdo. O critério de acerto do experimento exige o certificado da base
  de origem, então elas são contadas como erro sem o ser. **Zero atribuições
  genuinamente erradas.**
- Os 2 acertos do controle `uncertified_image` são o mesmo par: esconder o
  certificado da `picsum_456` ainda deixa o certificado idêntico da `picsum_128`
  no banco, que casa legitimamente. O controle tem 2 tentativas contaminadas em
  993; nas 991 válidas o resultado é **0 acertos, limite superior de 0,39% a 95%**.

Reporte a métrica com e sem o par. O `results/resumo_tcc_sem_duplicata.md` traz a
versão sem ele, e o efeito na manchete é pequeno: no estrato de alta confiança o
recall vai de 0,983 para 0,984 e a precisão permanece 1,000.

Registre também a limitação metodológica: um catálogo de imagens públicas pode
conter o mesmo conteúdo sob IDs diferentes, e um benchmark que trata cada ID como
uma identidade distinta precisa checar isso antes de chamar colisão de erro. A
checagem é barata — distância pHash par a par entre as bases.

## Resultados: as três medições concordam exatamente

O protocolo admitia divergência de até 1 ponto percentual entre os níveis. A
divergência observada foi **zero** nas três comparações.

| comparação | resultado |
| --- | --- |
| Par a par: ferramenta original (passo 2) × coluna `pair_matched` da simulação (passo 3) | idêntico: TP 28.296, FP 1.893, FN 6.275, TN 16.813; alta confiança TP 8.793, FP 4, FN 108, TN 7.843 |
| Pipeline: simulação em memória (passo 3) × e2e com PostgreSQL (passo 4) | idêntico nos 3 estratos **e nas 21 famílias, uma por uma** |
| Amostras sem veredito par a par | idêntico: 345 em dois caminhos independentes (216 puladas + 129 erros = 53.622 − 53.277) |

Manchete, pipeline completo, estrato de alta confiança:
**precisão 1,000 [0,999, 1,000]** e **recall 0,983 [0,980, 0,985]**, com 3 falsos
positivos em 8.786 predições positivas — todos da família `sepia`.

Tempos observados, em sequência, nunca em paralelo:

| etapa | workers | duração |
| --- | ---: | --- |
| Passo 3, acurácia | 4 | 2 h 34 min 09 s |
| Passo 3, latência | 1 | 28 min 06 s |
| Passo 4, end-to-end | 4 | 3 h 50 min 49 s |
| Passo 2, matriz par a par | 1 | 1 h 10 min 12 s |

Total de medição: **8 h 03 min**.

O passo 2 termina em `FAIL` com 129 erros de infraestrutura, conforme o protocolo
previa; o `report.json` é gravado antes da falha. O passo 4 termina em `PASS`,
com 0 falhas de certificação e 0 erros HTTP em 53.622 requisições.

## Procedência dos arquivos

| arquivo | origem |
| --- | --- |
| `ambiente.txt` | commit, tag, toolchain do container, host, metadados do dataset |
| `execucao.md` | este arquivo |
| `manifest.json` | passo 1, manifest regerado (993 bases, 53.622 amostras) |
| `source.lock.json` | passo 1, catálogo travado do Picsum |
| `calibration_base_ids.txt` | reconstrução das 100 bases de calibração |
| `duplicate_base_ids.txt` | o par de bases com conteúdo idêntico (`picsum_128`, `picsum_456`) |
| `resumo_tcc_sem_duplicata.md` | passo 3 com `--exclude results/duplicate_base_ids.txt` |
| `resumo_sem_calibracao.md` | passo 3 com `--exclude results/calibration_base_ids.txt` |
| `matrix_report.json` | passo 2, `TestDataset_FullMatrix` |
| `tcc_results.csv` | passo 3, rodada de acurácia (4 workers, stride 1) |
| `resumo_tcc.md` | passo 3, `scripts/tcc_summary.py` sobre o CSV de acurácia |
| `tcc_latency.csv` | passo 3, rodada de latência (1 worker, stride 20) |
| `resumo_latencia.md` | passo 3, `scripts/tcc_summary.py` sobre o CSV de latência |
| `e2e_report.json` | passo 4, `TestE2E_GeneratedDataset_Matrix` |
| `relatorio_completo.md` | relatório completo, fonte Markdown |
| `relatorio_completo.pdf` | relatório completo, 36 páginas |
| `../logs/*.log` | saída bruta de cada etapa |

No `resumo_tcc.md` use todas as seções exceto a de latência. No
`resumo_latencia.md` use só a de latência.

## Avisos para o texto

- A taxonomia tem mais positivos que negativos, então a acurácia está inflada
  por construção. Não a reporte isoladamente; a métrica principal é a precisão
  no estrato de alta confiança.
- Zero eventos num controle não é "nunca erra". Reporte o limite superior do
  intervalo de Wilson, que o script já calcula.
- Com cerca de 1.000 amostras por transformação, a meia-largura do intervalo de
  95% fica em no máximo ~3 pontos percentuais. Diferenças menores que isso entre
  famílias não devem ser interpretadas.
- O estrato limítrofe foi definido a partir de observações anteriores do próprio
  sistema. Declare isso e mostre também as métricas sem as bases de calibração.
- A certificação no passo 4 usa o endpoint legado, com `allowUnattested = true`,
  sem atestação de hardware e sem ancoragem. O e2e mede a verificação, não o
  fluxo de captura.
- O passo 2 termina em `FAIL` mesmo funcionando: a extração ORB falha em recortes
  pesados de imagens lisas e o teste conta isso como erro de infraestrutura. O
  `report.json` é gravado antes da falha. Quatro das 993 bases não produzem
  features ORB e foram certificadas só com pHash, o que a API também faz.
