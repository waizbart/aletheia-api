# Resumo do benchmark Aletheia

## Cenário

- Arquivo: `results/tcc_latency.csv`
- Bases distintas: **993**
- Amostras da taxonomia: **2682** em 16 famílias
- Controles `uncertified_image`: **50**
- Estrato de alta confiança: **894** amostras; limítrofe: **1788**
- Rótulos: **1888** positivos, **794** negativos

> A taxonomia tem mais positivos que negativos, então a acurácia está inflada por construção e não deve ser reportada isoladamente. A métrica principal é a precisão no estrato de alta confiança.

## Pipeline completo

Veredito do caminho completo de `verify.go`: SHA-256, pHash nas 4 rotações, pré-filtro LSH por bandas com Hamming ≤ 96, janela top-64 e correspondência visual. Uma amostra conta como acerto apenas quando o certificado devolvido é o da base correta. As linhas excluem os controles `uncertified_image`, reportados em seção própria.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 894 | 485 | 0 | 11 | 398 | 1.000 [0.992, 1.000] | 0.978 [0.961, 0.988] | 1.000 [0.990, 1.000] | 0.988 [0.978, 0.993] |
| Limítrofe | 1788 | 824 | 48 | 568 | 348 | 0.945 [0.928, 0.958] | 0.592 [0.566, 0.617] | 0.879 [0.843, 0.907] | 0.655 [0.633, 0.677] |
| Todas | 2682 | 1309 | 48 | 579 | 746 | 0.965 [0.953, 0.973] | 0.693 [0.672, 0.714] | 0.940 [0.921, 0.954] | 0.766 [0.750, 0.782] |

## Par a par

Veredito do matcher isolado: ORB, RANSAC, resíduo de cor e cobertura contra a base correta, sem busca. É o teto que o pipeline persegue.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 887 | 486 | 0 | 8 | 393 | 1.000 [0.992, 1.000] | 0.984 [0.968, 0.992] | 1.000 [0.990, 1.000] | 0.991 [0.982, 0.995] |
| Limítrofe | 1777 | 1011 | 48 | 373 | 345 | 0.955 [0.940, 0.966] | 0.730 [0.707, 0.753] | 0.878 [0.842, 0.907] | 0.763 [0.743, 0.782] |
| Todas | 2664 | 1497 | 48 | 381 | 738 | 0.969 [0.959, 0.976] | 0.797 [0.778, 0.815] | 0.939 [0.920, 0.954] | 0.839 [0.825, 0.852] |

## Por transformação

`acerto` é a fração de amostras cujo veredito bateu com o rótulo. A coluna `par a par` usa a mesma amostra com a referência correta entregue de mão beijada; a diferença entre as duas colunas é o custo do pré-filtro.

| família | transformação | rótulo | estrato | n | acerto pipeline [IC 95%] | acerto par a par [IC 95%] |
| --- | --- | --- | --- | ---: | --- | --- |
| brightness | brightness_minus10pct | match | limítrofe | 100 | 0.960 [0.902, 0.984] | 0.970 [0.915, 0.990] |
| brightness | brightness_plus10pct | match | limítrofe | 99 | 0.990 [0.945, 0.998] | 0.990 [0.945, 0.998] |
| color_invert | color_invert | rejeita | alta | 99 | 1.000 [0.963, 1.000] | 1.000 [0.962, 1.000] |
| content_overlay | content_overlay_15pct | rejeita | alta | 100 | 1.000 [0.963, 1.000] | 1.000 [0.963, 1.000] |
| content_overlay | content_overlay_30pct | rejeita | alta | 99 | 1.000 [0.963, 1.000] | 1.000 [0.963, 1.000] |
| crop_border | crop_border_15pct | match | limítrofe | 99 | 0.030 [0.010, 0.085] | 0.878 [0.798, 0.929] |
| crop_border | crop_border_5pct | match | limítrofe | 100 | 0.740 [0.646, 0.816] | 0.920 [0.850, 0.959] |
| downscale | downscale_0.33x | match | limítrofe | 99 | 0.051 [0.022, 0.113] | 0.051 [0.022, 0.113] |
| downscale | downscale_0.75x | match | limítrofe | 99 | 0.909 [0.836, 0.951] | 0.918 [0.847, 0.958] |
| downscale | downscale_256px | match | limítrofe | 99 | 0.061 [0.028, 0.126] | 0.062 [0.029, 0.128] |
| format_change | format_change_gif | match | limítrofe | 100 | 0.990 [0.946, 0.998] | 1.000 [0.963, 1.000] |
| format_change | format_change_tiff | match | limítrofe | 99 | 1.000 [0.963, 1.000] | 1.000 [0.963, 1.000] |
| heavy_crop | heavy_crop_40pct | rejeita | limítrofe | 99 | 1.000 [0.963, 1.000] | 1.000 [0.962, 1.000] |
| heavy_crop | heavy_crop_60pct | rejeita | alta | 100 | 1.000 [0.963, 1.000] | 1.000 [0.962, 1.000] |
| hue_shift | hue_shift_180deg | rejeita | limítrofe | 99 | 0.909 [0.836, 0.951] | 0.909 [0.836, 0.951] |
| hue_shift | hue_shift_60deg | rejeita | limítrofe | 99 | 0.869 [0.788, 0.922] | 0.867 [0.786, 0.921] |
| jpeg_recompress | jpeg_recompress_q20 | match | alta | 100 | 0.990 [0.946, 0.998] | 1.000 [0.963, 1.000] |
| jpeg_recompress | jpeg_recompress_q50 | match | alta | 99 | 0.990 [0.945, 0.998] | 0.990 [0.945, 0.998] |
| jpeg_recompress | jpeg_recompress_q90 | match | alta | 99 | 0.990 [0.945, 0.998] | 0.990 [0.945, 0.998] |
| noise_light | noise_sigma10 | match | alta | 99 | 0.949 [0.887, 0.978] | 0.959 [0.900, 0.984] |
| p3_as_srgb | p3_as_srgb_q70 | match | limítrofe | 100 | 0.890 [0.814, 0.937] | 0.899 [0.824, 0.944] |
| rotate_cardinal | rotate_180 | match | limítrofe | 99 | 0.455 [0.360, 0.552] | 0.455 [0.360, 0.552] |
| rotate_cardinal | rotate_90 | match | limítrofe | 100 | 0.430 [0.337, 0.528] | 0.430 [0.337, 0.528] |
| rotate_small | rotate_32deg | match | limítrofe | 99 | 0.010 [0.002, 0.055] | 0.888 [0.810, 0.936] |
| saturation_boost | saturation_boost_2.0x | rejeita | limítrofe | 99 | 0.737 [0.643, 0.814] | 0.737 [0.643, 0.814] |
| sharpen | sharpen_light | match | alta | 99 | 0.970 [0.915, 0.990] | 0.980 [0.929, 0.994] |
| upscale | upscale_2.0x | match | limítrofe | 100 | 0.760 [0.668, 0.833] | 0.760 [0.668, 0.833] |

> Com cerca de 1.000 amostras por transformação a meia-largura do intervalo de 95% fica em no máximo ~3 pontos percentuais. Diferenças menores que isso entre famílias não devem ser interpretadas.

## Controles negativos

| controle | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Imagem nunca certificada casou com algum certificado | 50 | 0 | 0.000 [0.000, 0.071] |
| Atribuída a outro certificado por correspondência visual | 2682 | 1 | 0.000 [0.000, 0.002] |
| Resolvida pelo SHA-256 para o certificado do próprio par (correto por construção) | 2682 | 0 | 0.000 [0.000, 0.001] |

> Zero eventos em 50 tentativas tem limite superior de **7.13%** a 95%. Reporte esse limite, não "nunca erra".

Atribuições visuais a outro certificado, por base de origem. Uma única base concentrando muitas é sinal de conteúdo duplicado no dataset, não de erro do sistema — verifique antes de reportar como falso positivo:

| base | ocorrências |
| --- | ---: |
| picsum_456 | 1 |

> O controle `uncertified_image` consulta a imagem de uma base com o certificado dela removido do banco, então o SHA-256 não resolve e a correspondência visual roda contra todos os outros certificados sem resposta correta disponível. O `different_image` do manifest não testa isso: ele devolve os bytes do par intactos, e o par é certificado, então o SHA-256 decide antes de qualquer comparação visual.

## Pré-filtro

Medido apenas nas amostras com rótulo positivo, onde existe um certificado correto para encontrar.

| medida | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Certificado correto chegou à lista de candidatos | 1888 | 1672 | 0.886 [0.870, 0.899] |
| Certificado correto dentro da janela top-64 | 1888 | 1672 | 0.886 [0.870, 0.899] |
| Certificado correto em primeiro lugar | 1888 | 1668 | 0.883 [0.868, 0.897] |

| distribuição | mediana | p95 | máx |
| --- | ---: | ---: | ---: |
| Candidatos avaliados por consulta | 1 | 3 | 6 |
| Distância pHash até o certificado correto | 8 | 118 | 138 |

Causa da perda, quando o certificado correto não chegou aos candidatos:

| causa | n | fração das positivas [IC 95%] |
| --- | ---: | --- |
| Distância pHash acima de 96 | 211 | 0.112 [0.098, 0.127] |
| Dentro de 96, mas sem colisão de banda | 5 | 0.003 [0.001, 0.006] |

> A segunda linha é a que mais importa na discussão: a sonda por bandas só encontra um certificado quando pelo menos um dos 32 bytes do pHash coincide exatamente. Um certificado pode estar dentro do limiar de Hamming e ainda assim nunca ser avaliado — nem um top-K maior nem um limiar mais folgado recuperam esses casos, só um esquema de bandas diferente.

Em **216** amostras positivas o certificado correto nunca chegou à correspondência visual. Dessas, **187** teriam casado par a par: são falsos negativos atribuíveis inteiramente ao pré-filtro.

| transformação | perdidas | das quais casariam par a par |
| --- | ---: | ---: |
| rotate_32deg | 98 | 86 |
| crop_border_15pct | 95 | 83 |
| crop_border_5pct | 22 | 18 |
| noise_sigma10 | 1 | 0 |

## Estágio que decidiu

| estágio | n | fração |
| --- | ---: | ---: |
| `visual_match` | 1358 | 0.497 |
| `no_match` | 990 | 0.362 |
| `prefilter_empty` | 367 | 0.134 |
| `orb_extract_failed` | 17 | 0.006 |

## Latência

| estágio | n | mediana (ms) | p95 (ms) | máx (ms) |
| --- | ---: | ---: | ---: | ---: |
| SHA-256 | 2732 | 0.1 | 0.4 | 1.5 |
| Consulta exata | 2732 | 0.0 | 0.0 | 0.0 |
| pHash (4 rotações) | 2732 | 218.0 | 289.3 | 687.7 |
| Extração ORB | 2732 | 26.1 | 42.3 | 80.2 |
| Pré-filtro LSH | 2732 | 0.5 | 0.6 | 1.3 |
| Correspondência visual | 2732 | 50.4 | 93.1 | 264.4 |
| Verificação completa | 2732 | 289.5 | 410.8 | 883.0 |

| veredito | n | mediana total (ms) | p95 total (ms) |
| --- | ---: | ---: | ---: |
| Casou | 1358 | 309.8 | 500.8 |
| Não casou | 1374 | 265.3 | 401.1 |

> Reporte a latência apenas da rodada em uma thread (`TCC_WORKERS=1`), informando o hardware. Números colhidos na rodada paralela medem disputa de CPU, não o custo do pipeline.
