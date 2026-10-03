# Resumo do benchmark Aletheia

## Cenário

- Arquivo: `results/tcc_results.csv`
- Bases distintas: **993**
- Amostras da taxonomia: **53622** em 21 famílias
- Controles `uncertified_image`: **993**
- Estrato de alta confiança: **16881** amostras; limítrofe: **36741**
- Rótulos: **34755** positivos, **18867** negativos

> A taxonomia tem mais positivos que negativos, então a acurácia está inflada por construção e não deve ser reportada isoladamente. A métrica principal é a precisão no estrato de alta confiança.

## Pipeline completo

Veredito do caminho completo de `verify.go`: SHA-256, pHash nas 4 rotações, pré-filtro LSH por bandas com Hamming ≤ 96, janela top-64 e correspondência visual. Uma amostra conta como acerto apenas quando o certificado devolvido é o da base correta. As linhas excluem os controles `uncertified_image`, reportados em seção própria.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 16881 | 8783 | 3 | 154 | 7941 | 1.000 [0.999, 1.000] | 0.983 [0.980, 0.985] | 1.000 [0.999, 1.000] | 0.991 [0.989, 0.992] |
| Limítrofe | 36741 | 15766 | 1884 | 10052 | 9039 | 0.893 [0.889, 0.898] | 0.611 [0.605, 0.617] | 0.828 [0.820, 0.834] | 0.675 [0.670, 0.680] |
| Todas | 53622 | 24549 | 1887 | 10206 | 16980 | 0.929 [0.925, 0.932] | 0.706 [0.702, 0.711] | 0.900 [0.896, 0.904] | 0.774 [0.771, 0.778] |

## Par a par

Veredito do matcher isolado: ORB, RANSAC, resíduo de cor e cobertura contra a base correta, sem busca. É o teto que o pipeline persegue.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 16748 | 8793 | 4 | 108 | 7843 | 1.000 [0.999, 1.000] | 0.988 [0.985, 0.990] | 0.999 [0.999, 1.000] | 0.993 [0.992, 0.994] |
| Limítrofe | 36529 | 19503 | 1889 | 6167 | 8970 | 0.912 [0.908, 0.915] | 0.760 [0.754, 0.765] | 0.826 [0.819, 0.833] | 0.779 [0.775, 0.784] |
| Todas | 53277 | 28296 | 1893 | 6275 | 16813 | 0.937 [0.935, 0.940] | 0.818 [0.814, 0.823] | 0.899 [0.894, 0.903] | 0.847 [0.844, 0.850] |

## Por transformação

`acerto` é a fração de amostras cujo veredito bateu com o rótulo. A coluna `par a par` usa a mesma amostra com a referência correta entregue de mão beijada; a diferença entre as duas colunas é o custo do pré-filtro.

| família | transformação | rótulo | estrato | n | acerto pipeline [IC 95%] | acerto par a par [IC 95%] |
| --- | --- | --- | --- | ---: | --- | --- |
| brightness | brightness_minus10pct | match | limítrofe | 993 | 0.951 [0.935, 0.962] | 0.956 [0.941, 0.967] |
| brightness | brightness_minus5pct | match | limítrofe | 993 | 0.987 [0.978, 0.992] | 0.992 [0.984, 0.996] |
| brightness | brightness_plus10pct | match | limítrofe | 993 | 0.945 [0.929, 0.957] | 0.949 [0.934, 0.961] |
| brightness | brightness_plus5pct | match | limítrofe | 993 | 0.988 [0.979, 0.993] | 0.993 [0.985, 0.997] |
| color_invert | color_invert | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_10pct | rejeita | limítrofe | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_15pct | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_20pct | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_30pct | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| crop_border | crop_border_10pct | match | limítrofe | 993 | 0.091 [0.074, 0.110] | 0.917 [0.898, 0.932] |
| crop_border | crop_border_15pct | match | limítrofe | 993 | 0.029 [0.020, 0.042] | 0.928 [0.910, 0.943] |
| crop_border | crop_border_20pct | rejeita | limítrofe | 993 | 1.000 [0.996, 1.000] | 0.998 [0.993, 0.999] |
| crop_border | crop_border_5pct | match | limítrofe | 993 | 0.684 [0.654, 0.712] | 0.914 [0.895, 0.930] |
| different_image | different_image | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| downscale | downscale_0.33x | match | limítrofe | 993 | 0.044 [0.033, 0.059] | 0.045 [0.034, 0.060] |
| downscale | downscale_0.5x | match | limítrofe | 993 | 0.401 [0.371, 0.432] | 0.404 [0.374, 0.435] |
| downscale | downscale_0.75x | match | limítrofe | 993 | 0.841 [0.817, 0.862] | 0.846 [0.822, 0.867] |
| downscale | downscale_160px | match | limítrofe | 993 | 0.000 [0.000, 0.004] | 0.000 [0.000, 0.004] |
| downscale | downscale_256px | match | limítrofe | 993 | 0.042 [0.031, 0.057] | 0.043 [0.032, 0.057] |
| format_change | format_change_bmp | match | limítrofe | 993 | 0.990 [0.982, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_gif | match | limítrofe | 993 | 0.990 [0.982, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_png | match | limítrofe | 993 | 0.990 [0.982, 0.995] | 0.995 [0.988, 0.998] |
| format_change | format_change_tiff | match | limítrofe | 993 | 0.990 [0.982, 0.995] | 0.995 [0.988, 0.998] |
| grayscale | grayscale | rejeita | limítrofe | 993 | 0.781 [0.755, 0.806] | 0.781 [0.754, 0.805] |
| heavy_crop | heavy_crop_40pct | rejeita | limítrofe | 993 | 1.000 [0.996, 1.000] | 0.998 [0.993, 0.999] |
| heavy_crop | heavy_crop_50pct | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 0.999 [0.994, 1.000] |
| heavy_crop | heavy_crop_60pct | rejeita | alta | 993 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| hue_shift | hue_shift_120deg | rejeita | limítrofe | 993 | 0.900 [0.880, 0.917] | 0.900 [0.880, 0.917] |
| hue_shift | hue_shift_180deg | rejeita | limítrofe | 993 | 0.915 [0.896, 0.931] | 0.915 [0.896, 0.931] |
| hue_shift | hue_shift_30deg | rejeita | limítrofe | 993 | 0.760 [0.733, 0.786] | 0.759 [0.732, 0.785] |
| hue_shift | hue_shift_60deg | rejeita | limítrofe | 993 | 0.874 [0.852, 0.893] | 0.874 [0.851, 0.893] |
| jpeg_recompress | jpeg_recompress_q10 | match | limítrofe | 993 | 0.916 [0.898, 0.932] | 0.921 [0.903, 0.936] |
| jpeg_recompress | jpeg_recompress_q20 | match | alta | 993 | 0.970 [0.957, 0.979] | 0.975 [0.963, 0.983] |
| jpeg_recompress | jpeg_recompress_q30 | match | alta | 993 | 0.979 [0.968, 0.986] | 0.984 [0.974, 0.990] |
| jpeg_recompress | jpeg_recompress_q50 | match | alta | 993 | 0.985 [0.975, 0.991] | 0.990 [0.981, 0.994] |
| jpeg_recompress | jpeg_recompress_q70 | match | alta | 993 | 0.987 [0.978, 0.992] | 0.992 [0.984, 0.996] |
| jpeg_recompress | jpeg_recompress_q90 | match | alta | 993 | 0.989 [0.980, 0.994] | 0.994 [0.987, 0.997] |
| localized_recolor | localized_recolor | rejeita | limítrofe | 993 | 0.714 [0.685, 0.741] | 0.713 [0.684, 0.740] |
| noise_light | noise_sigma10 | match | alta | 993 | 0.980 [0.969, 0.987] | 0.986 [0.976, 0.992] |
| noise_light | noise_sigma5 | match | alta | 993 | 0.985 [0.975, 0.991] | 0.990 [0.981, 0.994] |
| p3_as_srgb | p3_as_srgb_q70 | match | limítrofe | 993 | 0.840 [0.816, 0.861] | 0.844 [0.820, 0.866] |
| rotate_cardinal | rotate_180 | match | limítrofe | 993 | 0.437 [0.407, 0.468] | 0.440 [0.409, 0.471] |
| rotate_cardinal | rotate_270 | match | limítrofe | 993 | 0.912 [0.893, 0.928] | 0.917 [0.898, 0.933] |
| rotate_cardinal | rotate_90 | match | limítrofe | 993 | 0.397 [0.367, 0.428] | 0.399 [0.369, 0.430] |
| rotate_small | rotate_10deg | match | limítrofe | 993 | 0.092 [0.075, 0.111] | 0.869 [0.846, 0.888] |
| rotate_small | rotate_32deg | match | limítrofe | 993 | 0.005 [0.002, 0.012] | 0.850 [0.827, 0.871] |
| rotate_small | rotate_5deg | match | limítrofe | 993 | 0.680 [0.650, 0.708] | 0.878 [0.856, 0.897] |
| saturation_boost | saturation_boost_1.5x | rejeita | limítrofe | 993 | 0.345 [0.316, 0.376] | 0.342 [0.313, 0.372] |
| saturation_boost | saturation_boost_2.0x | rejeita | limítrofe | 993 | 0.812 [0.786, 0.835] | 0.811 [0.785, 0.834] |
| sepia | sepia | rejeita | alta | 993 | 0.997 [0.991, 0.999] | 0.997 [0.991, 0.999] |
| sharpen | sharpen_light | match | alta | 993 | 0.988 [0.979, 0.993] | 0.993 [0.985, 0.997] |
| upscale | upscale_1.5x | match | limítrofe | 993 | 0.809 [0.783, 0.832] | 0.813 [0.787, 0.836] |
| upscale | upscale_2.0x | match | limítrofe | 993 | 0.828 [0.803, 0.850] | 0.831 [0.807, 0.853] |
| whatsapp_like | whatsapp_like_960px_q40 | match | alta | 993 | 0.983 [0.973, 0.989] | 0.988 [0.979, 0.993] |

> Com cerca de 1.000 amostras por transformação a meia-largura do intervalo de 95% fica em no máximo ~3 pontos percentuais. Diferenças menores que isso entre famílias não devem ser interpretadas.

## Controles negativos

| controle | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Imagem nunca certificada casou com algum certificado | 993 | 2 | 0.002 [0.001, 0.007] |
| Atribuída a outro certificado por correspondência visual | 52629 | 27 | 0.001 [0.000, 0.001] |
| Resolvida pelo SHA-256 para o certificado do próprio par (correto por construção) | 53622 | 993 | 0.019 [0.017, 0.020] |

Atribuições visuais a outro certificado, por base de origem. Uma única base concentrando muitas é sinal de conteúdo duplicado no dataset, não de erro do sistema — verifique antes de reportar como falso positivo:

| base | ocorrências |
| --- | ---: |
| picsum_456 | 27 |

> O controle `uncertified_image` consulta a imagem de uma base com o certificado dela removido do banco, então o SHA-256 não resolve e a correspondência visual roda contra todos os outros certificados sem resposta correta disponível. O `different_image` do manifest não testa isso: ele devolve os bytes do par intactos, e o par é certificado, então o SHA-256 decide antes de qualquer comparação visual.

## Pré-filtro

Medido apenas nas amostras com rótulo positivo, onde existe um certificado correto para encontrar.

| medida | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Certificado correto chegou à lista de candidatos | 34755 | 30549 | 0.879 [0.876, 0.882] |
| Certificado correto dentro da janela top-64 | 34755 | 30549 | 0.879 [0.876, 0.882] |
| Certificado correto em primeiro lugar | 34755 | 30454 | 0.876 [0.873, 0.880] |

| distribuição | mediana | p95 | máx |
| --- | ---: | ---: | ---: |
| Candidatos avaliados por consulta | 1 | 3 | 12 |
| Distância pHash até o certificado correto | 10 | 116 | 138 |

Causa da perda, quando o certificado correto não chegou aos candidatos:

| causa | n | fração das positivas [IC 95%] |
| --- | ---: | --- |
| Distância pHash acima de 96 | 4125 | 0.119 [0.115, 0.122] |
| Dentro de 96, mas sem colisão de banda | 81 | 0.002 [0.002, 0.003] |

> A segunda linha é a que mais importa na discussão: a sonda por bandas só encontra um certificado quando pelo menos um dos 32 bytes do pHash coincide exatamente. Um certificado pode estar dentro do limiar de Hamming e ainda assim nunca ser avaliado — nem um top-K maior nem um limiar mais folgado recuperam esses casos, só um esquema de bandas diferente.

Em **4206** amostras positivas o certificado correto nunca chegou à correspondência visual. Dessas, **3721** teriam casado par a par: são falsos negativos atribuíveis inteiramente ao pré-filtro.

| transformação | perdidas | das quais casariam par a par |
| --- | ---: | ---: |
| rotate_32deg | 985 | 836 |
| crop_border_15pct | 962 | 886 |
| crop_border_10pct | 891 | 814 |
| rotate_10deg | 884 | 768 |
| crop_border_5pct | 251 | 224 |
| rotate_5deg | 227 | 192 |
| noise_sigma10 | 3 | 1 |
| downscale_256px | 1 | 0 |
| jpeg_recompress_q10 | 1 | 0 |
| downscale_160px | 1 | 0 |

## Estágio que decidiu

| estágio | n | fração |
| --- | ---: | ---: |
| `visual_match` | 26465 | 0.485 |
| `no_match` | 19988 | 0.366 |
| `prefilter_empty` | 6851 | 0.125 |
| `sha256` | 993 | 0.018 |
| `orb_extract_failed` | 318 | 0.006 |

## Latência

| estágio | n | mediana (ms) | p95 (ms) | máx (ms) |
| --- | ---: | ---: | ---: | ---: |
| SHA-256 | 54615 | 0.1 | 0.5 | 26.8 |
| Consulta exata | 54615 | 0.0 | 0.0 | 2.9 |
| pHash (4 rotações) | 54615 | 230.7 | 325.6 | 741.9 |
| Extração ORB | 54615 | 31.2 | 58.2 | 147.4 |
| Pré-filtro LSH | 54615 | 0.5 | 0.7 | 28.4 |
| Correspondência visual | 54615 | 71.8 | 149.8 | 528.8 |
| Verificação completa | 54615 | 339.9 | 499.8 | 1022.8 |

| veredito | n | mediana total (ms) | p95 total (ms) |
| --- | ---: | ---: | ---: |
| Casou | 27458 | 353.4 | 510.7 |
| Não casou | 27157 | 315.3 | 492.4 |

> Reporte a latência apenas da rodada em uma thread (`TCC_WORKERS=1`), informando o hardware. Números colhidos na rodada paralela medem disputa de CPU, não o custo do pipeline.
