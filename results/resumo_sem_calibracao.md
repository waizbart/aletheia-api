# Resumo do benchmark Aletheia

## Cenário

- Arquivo: `results/tcc_results.csv`
- Bases distintas: **893**
- Amostras da taxonomia: **48222** em 21 famílias
- Controles `uncertified_image`: **893**
- Estrato de alta confiança: **15181** amostras; limítrofe: **33041**
- Bases excluídas por `--exclude`: **100** (5500 linhas descartadas)
- Rótulos: **31255** positivos, **16967** negativos

> A taxonomia tem mais positivos que negativos, então a acurácia está inflada por construção e não deve ser reportada isoladamente. A métrica principal é a precisão no estrato de alta confiança.

## Pipeline completo

Veredito do caminho completo de `verify.go`: SHA-256, pHash nas 4 rotações, pré-filtro LSH por bandas com Hamming ≤ 96, janela top-64 e correspondência visual. Uma amostra conta como acerto apenas quando o certificado devolvido é o da base correta. As linhas excluem os controles `uncertified_image`, reportados em seção própria.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 15181 | 7899 | 2 | 138 | 7142 | 1.000 [0.999, 1.000] | 0.983 [0.980, 0.985] | 1.000 [0.999, 1.000] | 0.991 [0.989, 0.992] |
| Limítrofe | 33041 | 14210 | 1708 | 9008 | 8115 | 0.893 [0.888, 0.897] | 0.612 [0.606, 0.618] | 0.826 [0.819, 0.833] | 0.676 [0.671, 0.681] |
| Todas | 48222 | 22109 | 1710 | 9146 | 15257 | 0.928 [0.925, 0.931] | 0.707 [0.702, 0.712] | 0.899 [0.895, 0.904] | 0.775 [0.771, 0.779] |

## Par a par

Veredito do matcher isolado: ORB, RANSAC, resíduo de cor e cobertura contra a base correta, sem busca. É o teto que o pipeline persegue.

| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] | especificidade [IC 95%] | acurácia [IC 95%] |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| Alta confiança | 15070 | 7909 | 3 | 101 | 7057 | 1.000 [0.999, 1.000] | 0.987 [0.985, 0.990] | 1.000 [0.999, 1.000] | 0.993 [0.992, 0.994] |
| Limítrofe | 32879 | 17573 | 1713 | 5532 | 8061 | 0.911 [0.907, 0.915] | 0.761 [0.755, 0.766] | 0.825 [0.817, 0.832] | 0.780 [0.775, 0.784] |
| Todas | 47949 | 25482 | 1716 | 5633 | 15118 | 0.937 [0.934, 0.940] | 0.819 [0.815, 0.823] | 0.898 [0.893, 0.903] | 0.847 [0.843, 0.850] |

## Por transformação

`acerto` é a fração de amostras cujo veredito bateu com o rótulo. A coluna `par a par` usa a mesma amostra com a referência correta entregue de mão beijada; a diferença entre as duas colunas é o custo do pré-filtro.

| família | transformação | rótulo | estrato | n | acerto pipeline [IC 95%] | acerto par a par [IC 95%] |
| --- | --- | --- | --- | ---: | --- | --- |
| brightness | brightness_minus10pct | match | limítrofe | 893 | 0.951 [0.935, 0.963] | 0.955 [0.939, 0.967] |
| brightness | brightness_minus5pct | match | limítrofe | 893 | 0.988 [0.978, 0.993] | 0.992 [0.984, 0.996] |
| brightness | brightness_plus10pct | match | limítrofe | 893 | 0.945 [0.928, 0.958] | 0.949 [0.933, 0.962] |
| brightness | brightness_plus5pct | match | limítrofe | 893 | 0.988 [0.978, 0.993] | 0.992 [0.984, 0.996] |
| color_invert | color_invert | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_10pct | rejeita | limítrofe | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_15pct | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_20pct | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| content_overlay | content_overlay_30pct | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| crop_border | crop_border_10pct | match | limítrofe | 893 | 0.087 [0.071, 0.108] | 0.918 [0.898, 0.934] |
| crop_border | crop_border_15pct | match | limítrofe | 893 | 0.029 [0.020, 0.042] | 0.929 [0.910, 0.944] |
| crop_border | crop_border_20pct | rejeita | limítrofe | 893 | 1.000 [0.996, 1.000] | 0.998 [0.992, 0.999] |
| crop_border | crop_border_5pct | match | limítrofe | 893 | 0.693 [0.662, 0.723] | 0.918 [0.898, 0.934] |
| different_image | different_image | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| downscale | downscale_0.33x | match | limítrofe | 893 | 0.044 [0.032, 0.059] | 0.044 [0.032, 0.060] |
| downscale | downscale_0.5x | match | limítrofe | 893 | 0.401 [0.369, 0.433] | 0.404 [0.372, 0.437] |
| downscale | downscale_0.75x | match | limítrofe | 893 | 0.842 [0.817, 0.865] | 0.847 [0.822, 0.869] |
| downscale | downscale_160px | match | limítrofe | 893 | 0.000 [0.000, 0.004] | 0.000 [0.000, 0.004] |
| downscale | downscale_256px | match | limítrofe | 893 | 0.044 [0.032, 0.059] | 0.044 [0.032, 0.060] |
| format_change | format_change_bmp | match | limítrofe | 893 | 0.990 [0.981, 0.995] | 0.994 [0.987, 0.998] |
| format_change | format_change_gif | match | limítrofe | 893 | 0.990 [0.981, 0.995] | 0.994 [0.987, 0.998] |
| format_change | format_change_png | match | limítrofe | 893 | 0.990 [0.981, 0.995] | 0.994 [0.987, 0.998] |
| format_change | format_change_tiff | match | limítrofe | 893 | 0.990 [0.981, 0.995] | 0.994 [0.987, 0.998] |
| grayscale | grayscale | rejeita | limítrofe | 893 | 0.776 [0.748, 0.802] | 0.775 [0.747, 0.801] |
| heavy_crop | heavy_crop_40pct | rejeita | limítrofe | 893 | 1.000 [0.996, 1.000] | 0.998 [0.992, 0.999] |
| heavy_crop | heavy_crop_50pct | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 0.999 [0.994, 1.000] |
| heavy_crop | heavy_crop_60pct | rejeita | alta | 893 | 1.000 [0.996, 1.000] | 1.000 [0.996, 1.000] |
| hue_shift | hue_shift_120deg | rejeita | limítrofe | 893 | 0.898 [0.877, 0.916] | 0.898 [0.876, 0.916] |
| hue_shift | hue_shift_180deg | rejeita | limítrofe | 893 | 0.914 [0.894, 0.930] | 0.913 [0.893, 0.930] |
| hue_shift | hue_shift_30deg | rejeita | limítrofe | 893 | 0.757 [0.728, 0.784] | 0.756 [0.727, 0.783] |
| hue_shift | hue_shift_60deg | rejeita | limítrofe | 893 | 0.871 [0.848, 0.892] | 0.871 [0.847, 0.891] |
| jpeg_recompress | jpeg_recompress_q10 | match | limítrofe | 893 | 0.917 [0.897, 0.933] | 0.921 [0.902, 0.937] |
| jpeg_recompress | jpeg_recompress_q20 | match | alta | 893 | 0.971 [0.958, 0.980] | 0.975 [0.963, 0.984] |
| jpeg_recompress | jpeg_recompress_q30 | match | alta | 893 | 0.979 [0.967, 0.986] | 0.983 [0.972, 0.990] |
| jpeg_recompress | jpeg_recompress_q50 | match | alta | 893 | 0.984 [0.974, 0.991] | 0.989 [0.979, 0.994] |
| jpeg_recompress | jpeg_recompress_q70 | match | alta | 893 | 0.987 [0.977, 0.992] | 0.991 [0.982, 0.995] |
| jpeg_recompress | jpeg_recompress_q90 | match | alta | 893 | 0.989 [0.980, 0.994] | 0.993 [0.985, 0.997] |
| localized_recolor | localized_recolor | rejeita | limítrofe | 893 | 0.712 [0.682, 0.741] | 0.711 [0.681, 0.740] |
| noise_light | noise_sigma10 | match | alta | 893 | 0.980 [0.968, 0.987] | 0.985 [0.975, 0.991] |
| noise_light | noise_sigma5 | match | alta | 893 | 0.985 [0.975, 0.991] | 0.990 [0.981, 0.995] |
| p3_as_srgb | p3_as_srgb_q70 | match | limítrofe | 893 | 0.844 [0.819, 0.867] | 0.848 [0.823, 0.870] |
| rotate_cardinal | rotate_180 | match | limítrofe | 893 | 0.446 [0.413, 0.478] | 0.448 [0.416, 0.481] |
| rotate_cardinal | rotate_270 | match | limítrofe | 893 | 0.914 [0.894, 0.930] | 0.918 [0.898, 0.934] |
| rotate_cardinal | rotate_90 | match | limítrofe | 893 | 0.398 [0.366, 0.430] | 0.400 [0.368, 0.433] |
| rotate_small | rotate_10deg | match | limítrofe | 893 | 0.094 [0.077, 0.115] | 0.872 [0.848, 0.892] |
| rotate_small | rotate_32deg | match | limítrofe | 893 | 0.003 [0.001, 0.010] | 0.851 [0.826, 0.872] |
| rotate_small | rotate_5deg | match | limítrofe | 893 | 0.688 [0.656, 0.717] | 0.879 [0.856, 0.898] |
| saturation_boost | saturation_boost_1.5x | rejeita | limítrofe | 893 | 0.352 [0.321, 0.384] | 0.348 [0.318, 0.380] |
| saturation_boost | saturation_boost_2.0x | rejeita | limítrofe | 893 | 0.807 [0.780, 0.832] | 0.807 [0.780, 0.831] |
| sepia | sepia | rejeita | alta | 893 | 0.998 [0.992, 0.999] | 0.998 [0.992, 0.999] |
| sharpen | sharpen_light | match | alta | 893 | 0.988 [0.978, 0.993] | 0.992 [0.984, 0.996] |
| upscale | upscale_1.5x | match | limítrofe | 893 | 0.813 [0.786, 0.837] | 0.817 [0.790, 0.841] |
| upscale | upscale_2.0x | match | limítrofe | 893 | 0.825 [0.799, 0.849] | 0.828 [0.802, 0.851] |
| whatsapp_like | whatsapp_like_960px_q40 | match | alta | 893 | 0.983 [0.972, 0.990] | 0.988 [0.978, 0.993] |

> Com cerca de 1.000 amostras por transformação a meia-largura do intervalo de 95% fica em no máximo ~3 pontos percentuais. Diferenças menores que isso entre famílias não devem ser interpretadas.

## Controles negativos

| controle | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Imagem nunca certificada casou com algum certificado | 893 | 2 | 0.002 [0.001, 0.008] |
| Atribuída a outro certificado por correspondência visual | 47329 | 27 | 0.001 [0.000, 0.001] |
| Resolvida pelo SHA-256 para o certificado do próprio par (correto por construção) | 48222 | 893 | 0.019 [0.017, 0.020] |

Atribuições visuais a outro certificado, por base de origem. Uma única base concentrando muitas é sinal de conteúdo duplicado no dataset, não de erro do sistema — verifique antes de reportar como falso positivo:

| base | ocorrências |
| --- | ---: |
| picsum_456 | 27 |

> O controle `uncertified_image` consulta a imagem de uma base com o certificado dela removido do banco, então o SHA-256 não resolve e a correspondência visual roda contra todos os outros certificados sem resposta correta disponível. O `different_image` do manifest não testa isso: ele devolve os bytes do par intactos, e o par é certificado, então o SHA-256 decide antes de qualquer comparação visual.

## Pré-filtro

Medido apenas nas amostras com rótulo positivo, onde existe um certificado correto para encontrar.

| medida | n | eventos | taxa [IC 95%] |
| --- | ---: | ---: | --- |
| Certificado correto chegou à lista de candidatos | 31255 | 27482 | 0.879 [0.876, 0.883] |
| Certificado correto dentro da janela top-64 | 31255 | 27482 | 0.879 [0.876, 0.883] |
| Certificado correto em primeiro lugar | 31255 | 27391 | 0.876 [0.873, 0.880] |

| distribuição | mediana | p95 | máx |
| --- | ---: | ---: | ---: |
| Candidatos avaliados por consulta | 1 | 3 | 12 |
| Distância pHash até o certificado correto | 10 | 116 | 138 |

Causa da perda, quando o certificado correto não chegou aos candidatos:

| causa | n | fração das positivas [IC 95%] |
| --- | ---: | --- |
| Distância pHash acima de 96 | 3694 | 0.118 [0.115, 0.122] |
| Dentro de 96, mas sem colisão de banda | 79 | 0.003 [0.002, 0.003] |

> A segunda linha é a que mais importa na discussão: a sonda por bandas só encontra um certificado quando pelo menos um dos 32 bytes do pHash coincide exatamente. Um certificado pode estar dentro do limiar de Hamming e ainda assim nunca ser avaliado — nem um top-K maior nem um limiar mais folgado recuperam esses casos, só um esquema de bandas diferente.

Em **3773** amostras positivas o certificado correto nunca chegou à correspondência visual. Dessas, **3347** teriam casado par a par: são falsos negativos atribuíveis inteiramente ao pré-filtro.

| transformação | perdidas | das quais casariam par a par |
| --- | ---: | ---: |
| rotate_32deg | 887 | 754 |
| crop_border_15pct | 865 | 799 |
| crop_border_10pct | 804 | 737 |
| rotate_10deg | 792 | 692 |
| crop_border_5pct | 222 | 197 |
| rotate_5deg | 200 | 167 |
| noise_sigma10 | 2 | 1 |
| jpeg_recompress_q10 | 1 | 0 |

## Estágio que decidiu

| estágio | n | fração |
| --- | ---: | ---: |
| `visual_match` | 23848 | 0.486 |
| `no_match` | 17970 | 0.366 |
| `prefilter_empty` | 6152 | 0.125 |
| `sha256` | 893 | 0.018 |
| `orb_extract_failed` | 252 | 0.005 |

## Latência

| estágio | n | mediana (ms) | p95 (ms) | máx (ms) |
| --- | ---: | ---: | ---: | ---: |
| SHA-256 | 49115 | 0.1 | 0.5 | 26.8 |
| Consulta exata | 49115 | 0.0 | 0.0 | 2.9 |
| pHash (4 rotações) | 49115 | 230.8 | 325.7 | 741.9 |
| Extração ORB | 49115 | 31.3 | 58.2 | 135.3 |
| Pré-filtro LSH | 49115 | 0.5 | 0.7 | 28.4 |
| Correspondência visual | 49115 | 71.8 | 149.7 | 528.8 |
| Verificação completa | 49115 | 340.1 | 499.3 | 1022.8 |

| veredito | n | mediana total (ms) | p95 total (ms) |
| --- | ---: | ---: | ---: |
| Casou | 24741 | 353.8 | 510.4 |
| Não casou | 24374 | 315.4 | 491.9 |

> Reporte a latência apenas da rodada em uma thread (`TCC_WORKERS=1`), informando o hardware. Números colhidos na rodada paralela medem disputa de CPU, não o custo do pipeline.
