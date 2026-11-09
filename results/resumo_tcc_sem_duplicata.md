# Resumo do benchmark Aletheia

## Cenário

- Arquivo: `results/tcc_results.csv`
- Bases distintas: **991**
- Amostras da taxonomia: **53514** em 21 famílias
- Controles `uncertified_image`: **991**
- Estrato de alta confiança: **16847** amostras; limítrofe: **36667**
- Bases excluídas por `--exclude`: **2** (110 linhas descartadas)
- Rótulos: **34685** positivos, **18829** negativos

> A taxonomia tem mais positivos que negativos, então a acurácia está inflada por construção e não deve ser reportada isoladamente. A métrica principal é a precisão no estrato de alta confiança.

## Pipeline completo

Veredito do caminho completo de `verify.go`: SHA-256, pHash nas 4 rotações, pré-filtro LSH por bandas com Hamming ≤ 96, janela top-64 e correspondência visual. Uma amostra conta como acerto apenas quando o certificado devolvido é o da base correta. As linhas excluem os controles `uncertified_image`, reportados em seção própria.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 16847 | 8774 | 3 | 145 | 7925 | 1.000 [0.999, 1.000] | 0.984 [0.981, 0.986] | 1.000 [0.999, 1.000] | 0.991 [0.990, 0.993] |
| Limítrofe | 36667 | 15749 | 1883 | 10017 | 9018 | 0.893 [0.889, 0.898] | 0.611 [0.605, 0.617] | 0.827 [0.820, 0.834] | 0.675 [0.671, 0.680] |
| Todas | 53514 | 24523 | 1886 | 10162 | 16943 | 0.929 [0.925, 0.932] | 0.707 [0.702, 0.712] | 0.900 [0.895, 0.904] | 0.775 [0.771, 0.778] |

## Par a par

Veredito do matcher isolado: ORB, RANSAC, resíduo de cor e cobertura contra a base correta, sem busca. É o teto que o pipeline persegue.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 16714 | 8775 | 4 | 108 | 7827 | 1.000 [0.999, 1.000] | 0.988 [0.985, 0.990] | 0.999 [0.999, 1.000] | 0.993 [0.992, 0.994] |
| Limítrofe | 36455 | 19461 | 1887 | 6157 | 8950 | 0.912 [0.908, 0.915] | 0.760 [0.754, 0.765] | 0.826 [0.819, 0.833] | 0.779 [0.775, 0.784] |
| Todas | 53169 | 28236 | 1891 | 6265 | 16777 | 0.937 [0.934, 0.940] | 0.818 [0.814, 0.822] | 0.899 [0.894, 0.903] | 0.847 [0.844, 0.850] |

## Por transformação

`acerto` é a fração de amostras cujo veredito bateu com o rótulo. A coluna `par a par` usa a mesma amostra com a referência correta entregue de mão beijada; a diferença entre as duas colunas é o custo do pré-filtro.

| família | transformação | rótulo | estrato | n | acerto pipeline [IC 95%] | acerto par a par [IC 95%] |
| --- | --- | --- | --- | ---: | --- | --- |
| brightness | brightness_minus10pct | match | limítrofe | 991 | 0.952 [0.936, 0.963] | 0.955 [0.941, 0.967] |
| brightness | brightness_minus5pct | match | limítrofe | 991 | 0.988 [0.979, 0.993] | 0.992 [0.984, 0.996] |
| brightness | brightness_plus10pct | match | limítrofe | 991 | 0.946 [0.930, 0.958] | 0.949 [0.934, 0.961] |
| brightness | brightness_plus5pct | match | limítrofe | 991 | 0.989 [0.980, 0.994] | 0.993 [0.985, 0.997] |
| color_invert | color_invert | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_10pct | rejeita | limítrofe | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_15pct | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_20pct | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_30pct | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| crop_border | crop_border_10pct | match | limítrofe | 991 | 0.091 [0.074, 0.110] | 0.917 [0.898, 0.932] |
| crop_border | crop_border_15pct | match | limítrofe | 991 | 0.029 [0.020, 0.042] | 0.928 [0.910, 0.942] |
| crop_border | crop_border_20pct | rejeita | limítrofe | 991 | 1.000 [0.996, 1.000] | 0.998 [0.993, 0.999] |
| crop_border | crop_border_5pct | match | limítrofe | 991 | 0.685 [0.656, 0.713] | 0.914 [0.895, 0.930] |
| different_image | different_image | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| downscale | downscale_0.33x | match | limítrofe | 991 | 0.044 [0.033, 0.059] | 0.045 [0.034, 0.060] |
| downscale | downscale_0.5x | match | limítrofe | 991 | 0.401 [0.371, 0.431] | 0.403 [0.373, 0.434] |
| downscale | downscale_0.75x | match | limítrofe | 991 | 0.842 [0.818, 0.863] | 0.846 [0.822, 0.867] |
| downscale | downscale_160px | match | limítrofe | 991 | 0.000 [0.000, 0.004] | 0.000 [0.000, 0.004] |
| downscale | downscale_256px | match | limítrofe | 991 | 0.042 [0.032, 0.057] | 0.043 [0.032, 0.057] |
| format_change | format_change_bmp | match | limítrofe | 991 | 0.991 [0.983, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_gif | match | limítrofe | 991 | 0.991 [0.983, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_png | match | limítrofe | 991 | 0.991 [0.983, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_tiff | match | limítrofe | 991 | 0.991 [0.983, 0.995] | 0.995 [0.988, 0.998] |
| grayscale | grayscale | rejeita | limítrofe | 991 | 0.781 [0.754, 0.806] | 0.780 [0.753, 0.805] |
| heavy_crop | heavy_crop_40pct | rejeita | limítrofe | 991 | 1.000 [0.996, 1.000] | 0.998 [0.993, 0.999] |
| heavy_crop | heavy_crop_50pct | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 0.999 [0.994, 1.000] |
| heavy_crop | heavy_crop_60pct | rejeita | alta | 991 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| hue_shift | hue_shift_120deg | rejeita | limítrofe | 991 | 0.900 [0.880, 0.917] | 0.900 [0.879, 0.917] |
| hue_shift | hue_shift_180deg | rejeita | limítrofe | 991 | 0.915 [0.896, 0.931] | 0.915 [0.896, 0.931] |
| hue_shift | hue_shift_30deg | rejeita | limítrofe | 991 | 0.760 [0.732, 0.785] | 0.759 [0.731, 0.785] |
| hue_shift | hue_shift_60deg | rejeita | limítrofe | 991 | 0.874 [0.852, 0.893] | 0.873 [0.851, 0.893] |
| jpeg_recompress | jpeg_recompress_q10 | match | limítrofe | 991 | 0.917 [0.898, 0.933] | 0.921 [0.902, 0.936] |
| jpeg_recompress | jpeg_recompress_q20 | match | alta | 991 | 0.971 [0.958, 0.980] | 0.975 [0.963, 0.983] |
| jpeg_recompress | jpeg_recompress_q30 | match | alta | 991 | 0.980 [0.969, 0.987] | 0.984 [0.974, 0.990] |
| jpeg_recompress | jpeg_recompress_q50 | match | alta | 991 | 0.986 [0.976, 0.992] | 0.990 [0.981, 0.994] |
| jpeg_recompress | jpeg_recompress_q70 | match | alta | 991 | 0.988 [0.979, 0.993] | 0.992 [0.984, 0.996] |
| jpeg_recompress | jpeg_recompress_q90 | match | alta | 991 | 0.990 [0.982, 0.995] | 0.994 [0.987, 0.997] |
| localized_recolor | localized_recolor | rejeita | limítrofe | 991 | 0.713 [0.684, 0.741] | 0.712 [0.683, 0.740] |
| noise_light | noise_sigma10 | match | alta | 991 | 0.981 [0.970, 0.988] | 0.986 [0.976, 0.992] |
| noise_light | noise_sigma5 | match | alta | 991 | 0.986 [0.976, 0.992] | 0.990 [0.981, 0.994] |
| p3_as_srgb | p3_as_srgb_q70 | match | limítrofe | 991 | 0.841 [0.816, 0.862] | 0.844 [0.820, 0.865] |
| rotate_cardinal | rotate_180 | match | limítrofe | 991 | 0.437 [0.406, 0.468] | 0.439 [0.408, 0.470] |
| rotate_cardinal | rotate_270 | match | limítrofe | 991 | 0.913 [0.894, 0.929] | 0.917 [0.898, 0.933] |
| rotate_cardinal | rotate_90 | match | limítrofe | 991 | 0.397 [0.367, 0.427] | 0.398 [0.368, 0.429] |
| rotate_small | rotate_10deg | match | limítrofe | 991 | 0.092 [0.075, 0.111] | 0.870 [0.848, 0.890] |
| rotate_small | rotate_32deg | match | limítrofe | 991 | 0.005 [0.002, 0.012] | 0.850 [0.826, 0.871] |
| rotate_small | rotate_5deg | match | limítrofe | 991 | 0.680 [0.650, 0.708] | 0.877 [0.855, 0.896] |
| saturation_boost | saturation_boost_1.5x | rejeita | limítrofe | 991 | 0.345 [0.316, 0.375] | 0.342 [0.314, 0.373] |
| saturation_boost | saturation_boost_2.0x | rejeita | limítrofe | 991 | 0.811 [0.786, 0.834] | 0.811 [0.785, 0.834] |
| sepia | sepia | rejeita | alta | 991 | 0.997 [0.991, 0.999] | 0.997 [0.991, 0.999] |
| sharpen | sharpen_light | match | alta | 991 | 0.989 [0.980, 0.994] | 0.993 [0.985, 0.997] |
| upscale | upscale_1.5x | match | limítrofe | 991 | 0.809 [0.784, 0.833] | 0.813 [0.787, 0.836] |
| upscale | upscale_2.0x | match | limítrofe | 991 | 0.829 [0.805, 0.852] | 0.833 [0.808, 0.855] |
| whatsapp_like | whatsapp_like_960px_q40 | match | alta | 991 | 0.984 [0.974, 0.990] | 0.988 [0.979, 0.993] |

> Com cerca de 1.000 amostras por transformação a meia-largura do intervalo de 95% fica em no máximo ~3 pontos percentuais. Diferenças menores que isso entre famílias não devem ser interpretadas.

## Controles negativos

| controle | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Imagem nunca certificada casou com algum certificado | 991 | 0 | 0.000 [0.000, 0.004] |
| Atribuída a outro certificado por correspondência visual | 52523 | 0 | 0.000 [0.000, 0.000] |
| Resolvida pelo SHA-256 para o certificado do próprio par (correto por construção) | 53514 | 991 | 0.019 [0.017, 0.020] |

> Zero eventos em 991 tentativas tem limite superior de **0.39%** a 95%. Reporte esse limite, não "nunca erra".

> O controle `uncertified_image` consulta a imagem de uma base com o certificado dela removido do banco, então o SHA-256 não resolve e a correspondência visual roda contra todos os outros certificados sem resposta correta disponível. O `different_image` do manifest não testa isso: ele devolve os bytes do par intactos, e o par é certificado, então o SHA-256 decide antes de qualquer comparação visual.

## Pré-filtro

Medido apenas nas amostras com rótulo positivo, onde existe um certificado correto para encontrar.

| medida | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Certificado correto chegou à lista de candidatos | 34685 | 30489 | 0.879 [0.876, 0.882] |
| Certificado correto dentro da janela top-64 | 34685 | 30489 | 0.879 [0.876, 0.882] |
| Certificado correto em primeiro lugar | 34685 | 30424 | 0.877 [0.874, 0.881] |

| distribuição | mediana | p95 | máx |
| --- | ---: | ---: | ---: |
| Candidatos avaliados por consulta | 1 | 3 | 12 |
| Distância pHash até o certificado correto | 10 | 116 | 138 |

Causa da perda, quando o certificado correto não chegou aos candidatos:

| causa | n | fração das positivas [IC 95%] |
| --- | ---: | --- |
| Distância pHash acima de 96 | 4117 | 0.119 [0.115, 0.122] |
| Dentro de 96, mas sem colisão de banda | 79 | 0.002 [0.002, 0.003] |

> A segunda linha é a que mais importa na discussão: a sonda por bandas só encontra um certificado quando pelo menos um dos 32 bytes do pHash coincide exatamente. Um certificado pode estar dentro do limiar de Hamming e ainda assim nunca ser avaliado — nem um top-K maior nem um limiar mais folgado recuperam esses casos, só um esquema de bandas diferente.

Em **4196** amostras positivas o certificado correto nunca chegou à correspondência visual. Dessas, **3713** teriam casado par a par: são falsos negativos atribuíveis inteiramente ao pré-filtro.

| transformação | perdidas | das quais casariam par a par |
| --- | ---: | ---: |
| rotate_32deg | 983 | 834 |
| crop_border_15pct | 960 | 884 |
| crop_border_10pct | 889 | 812 |
| rotate_10deg | 882 | 768 |
| crop_border_5pct | 249 | 222 |
| rotate_5deg | 227 | 192 |
| noise_sigma10 | 3 | 1 |
| downscale_256px | 1 | 0 |
| jpeg_recompress_q10 | 1 | 0 |
| downscale_160px | 1 | 0 |

## Estágio que decidiu

| estágio | n | fração |
| --- | ---: | ---: |
| `visual_match` | 26409 | 0.485 |
| `no_match` | 19954 | 0.366 |
| `prefilter_empty` | 6833 | 0.125 |
| `sha256` | 991 | 0.018 |
| `orb_extract_failed` | 318 | 0.006 |

## Latência

| estágio | n | mediana (ms) | p95 (ms) | máx (ms) |
| --- | ---: | ---: | ---: | ---: |
| SHA-256 | 54505 | 0.1 | 0.5 | 26.8 |
| Consulta exata | 54505 | 0.0 | 0.0 | 2.9 |
| pHash (4 rotações) | 54505 | 230.7 | 325.6 | 741.9 |
| Extração ORB | 54505 | 31.2 | 58.2 | 147.4 |
| Pré-filtro LSH | 54505 | 0.5 | 0.7 | 28.4 |
| Correspondência visual | 54505 | 71.8 | 149.6 | 528.8 |
| Verificação completa | 54505 | 339.9 | 499.5 | 1022.8 |

| veredito | n | mediana total (ms) | p95 total (ms) |
| --- | ---: | ---: | ---: |
| Casou | 27400 | 353.4 | 510.8 |
| Não casou | 27105 | 315.3 | 491.7 |

> Reporte a latência apenas da rodada em uma thread (`TCC_WORKERS=1`), informando o hardware. Números colhidos na rodada paralela medem disputa de CPU, não o custo do pipeline.
