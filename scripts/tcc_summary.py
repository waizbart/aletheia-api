#!/usr/bin/env python3
"""Turn a TestTCC_PipelineSimulation CSV into the Markdown tables of chapter 7.

Reads the per-sample records written by tests/feature/tcc_experiment_test.go and
emits, with Wilson 95% intervals on every rate:

  * the scenario header (bases, samples, strata sizes);
  * the full-pipeline confusion matrix per confidence stratum;
  * the isolated pairwise confusion matrix per stratum, for side-by-side reading;
  * a per-family breakdown of both;
  * the negative controls (uncertified images, wrong-certificate attributions);
  * which stage decided each verdict;
  * pre-filter diagnostics, which attribute the pipeline-versus-pairwise gap;
  * latency per stage (median and p95).

Standard library only.

Usage:
    python3 scripts/tcc_summary.py results/tcc_results.csv --out results/resumo_tcc.md
    python3 scripts/tcc_summary.py results/tcc_latency.csv --out results/resumo_latencia.md

    # Metrics recomputed without the bases used to calibrate the taxonomy:
    python3 scripts/tcc_summary.py results/tcc_results.csv \
        --exclude results/calibration_base_ids.txt --out results/resumo_sem_calibracao.md
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import sys
from collections import Counter, defaultdict

HIGH = "high"
BORDERLINE = "borderline"
CONTROL_FAMILY = "uncertified_image"


# --------------------------------------------------------------------------- #
# Statistics
# --------------------------------------------------------------------------- #

def wilson(successes: int, total: int, z: float = 1.959963985) -> tuple[float, float, float]:
    """Return (point, low, high) for a binomial proportion.

    The Wilson score interval is used rather than the normal approximation
    because several cells here sit at or near 0 and 1, where the normal
    approximation produces bounds outside [0, 1] or a zero-width interval. A
    zero-success cell is exactly the case that matters for a certifier: 0/1000
    false positives has an upper bound near 0.4%, which is the honest claim,
    not "never".
    """
    if total == 0:
        return (float("nan"), float("nan"), float("nan"))
    p = successes / total
    denom = 1 + z * z / total
    center = (p + z * z / (2 * total)) / denom
    half = (z / denom) * math.sqrt(p * (1 - p) / total + z * z / (4 * total * total))
    return (p, max(0.0, center - half), min(1.0, center + half))


def fmt_rate(successes: int, total: int) -> str:
    """Format a rate as 'point [low, high]', or '—' when undefined."""
    if total == 0:
        return "—"
    p, lo, hi = wilson(successes, total)
    return f"{p:.3f} [{lo:.3f}, {hi:.3f}]"


def percentile(values: list[float], q: float) -> float:
    """Linear-interpolation percentile, q in [0, 1]. Assumes values is sorted."""
    if not values:
        return float("nan")
    if len(values) == 1:
        return values[0]
    pos = q * (len(values) - 1)
    lo = math.floor(pos)
    hi = math.ceil(pos)
    if lo == hi:
        return values[int(pos)]
    return values[lo] + (values[hi] - values[lo]) * (pos - lo)


# --------------------------------------------------------------------------- #
# Confusion matrix
# --------------------------------------------------------------------------- #

class Confusion:
    __slots__ = ("tp", "fp", "fn", "tn")

    def __init__(self) -> None:
        self.tp = self.fp = self.fn = self.tn = 0

    def add(self, expected: bool, predicted: bool) -> None:
        if expected:
            if predicted:
                self.tp += 1
            else:
                self.fn += 1
        else:
            if predicted:
                self.fp += 1
            else:
                self.tn += 1

    @property
    def total(self) -> int:
        return self.tp + self.fp + self.fn + self.tn

    def row(self, label: str) -> str:
        return (
            f"| {label} | {self.total} | {self.tp} | {self.fp} | {self.fn} | {self.tn} "
            f"| {fmt_rate(self.tp, self.tp + self.fp)} "
            f"| {fmt_rate(self.tp, self.tp + self.fn)} "
            f"| {fmt_rate(self.tn, self.tn + self.fp)} "
            f"| {fmt_rate(self.tp + self.tn, self.total)} |"
        )


CONF_HEADER = (
    "| estrato | n | TP | FP | FN | TN | precisão [IC 95%] | recall [IC 95%] "
    "| especificidade [IC 95%] | acurácia [IC 95%] |\n"
    "| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |"
)


# --------------------------------------------------------------------------- #
# Loading
# --------------------------------------------------------------------------- #

def as_bool(v: str) -> bool:
    return str(v).strip().lower() == "true"


def as_float(v: str) -> float | None:
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def as_int(v: str) -> int | None:
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def load(path: str, excluded: set[str]) -> tuple[list[dict], int]:
    rows: list[dict] = []
    skipped = 0
    with open(path, newline="", encoding="utf-8") as fh:
        reader = csv.DictReader(fh)
        required = {"sample_id", "base_id", "expected_match", "pipeline_matched"}
        missing = required - set(reader.fieldnames or [])
        if missing:
            sys.exit(f"{path}: missing required columns: {', '.join(sorted(missing))}")
        for raw in reader:
            if raw["base_id"] in excluded:
                skipped += 1
                continue
            rows.append(raw)
    return rows, skipped


def read_exclude(spec: str | None) -> set[str]:
    """Accept either a path with one base id per line or a comma-separated list."""
    if not spec:
        return set()
    if os.path.exists(spec):
        with open(spec, encoding="utf-8") as fh:
            return {
                line.strip()
                for line in fh
                if line.strip() and not line.lstrip().startswith("#")
            }
    return {part.strip() for part in spec.split(",") if part.strip()}


# --------------------------------------------------------------------------- #
# Sections
# --------------------------------------------------------------------------- #

def strata_of(row: dict) -> str:
    return HIGH if row.get("confidence") == HIGH else BORDERLINE


def section_scenario(out: list[str], path: str, rows: list[dict], skipped: int,
                     excluded: set[str]) -> None:
    samples = [r for r in rows if not as_bool(r.get("is_control", "false"))]
    controls = [r for r in rows if as_bool(r.get("is_control", "false"))]
    bases = {r["base_id"] for r in rows}
    families = {r.get("family", "") for r in samples}
    high = sum(1 for r in samples if strata_of(r) == HIGH)

    out.append("## Cenário\n")
    out.append(f"- Arquivo: `{path}`")
    out.append(f"- Bases distintas: **{len(bases)}**")
    out.append(f"- Amostras da taxonomia: **{len(samples)}** em {len(families)} famílias")
    out.append(f"- Controles `{CONTROL_FAMILY}`: **{len(controls)}**")
    out.append(f"- Estrato de alta confiança: **{high}** amostras; "
               f"limítrofe: **{len(samples) - high}**")
    if excluded:
        out.append(f"- Bases excluídas por `--exclude`: **{len(excluded)}** "
                   f"({skipped} linhas descartadas)")
    positives = sum(1 for r in samples if as_bool(r["expected_match"]))
    out.append(f"- Rótulos: **{positives}** positivos, **{len(samples) - positives}** negativos")
    out.append("")
    out.append("> A taxonomia tem mais positivos que negativos, então a acurácia está "
               "inflada por construção e não deve ser reportada isoladamente. "
               "A métrica principal é a precisão no estrato de alta confiança.")
    out.append("")


def confusion_by_stratum(rows: list[dict], predicted_col: str,
                         require_evaluated: str | None = None) -> dict[str, Confusion]:
    out = {HIGH: Confusion(), BORDERLINE: Confusion(), "todas": Confusion()}
    for r in rows:
        if as_bool(r.get("is_control", "false")):
            continue
        if require_evaluated and not as_bool(r.get(require_evaluated, "false")):
            continue
        expected = as_bool(r["expected_match"])
        predicted = as_bool(r[predicted_col])
        out[strata_of(r)].add(expected, predicted)
        out["todas"].add(expected, predicted)
    return out


def section_confusion(out: list[str], title: str, note: str,
                      conf: dict[str, Confusion]) -> None:
    out.append(f"## {title}\n")
    out.append(note)
    out.append("")
    out.append(CONF_HEADER)
    out.append(conf[HIGH].row("Alta confiança"))
    out.append(conf[BORDERLINE].row("Limítrofe"))
    out.append(conf["todas"].row("Todas"))
    out.append("")


def section_families(out: list[str], rows: list[dict]) -> None:
    pipe: dict[tuple[str, str], Confusion] = defaultdict(Confusion)
    pair: dict[tuple[str, str], Confusion] = defaultdict(Confusion)
    expects: dict[str, set[bool]] = defaultdict(set)
    confs: dict[str, set[str]] = defaultdict(set)

    for r in rows:
        if as_bool(r.get("is_control", "false")):
            continue
        fam = r.get("family", "")
        name = r.get("transform", "")
        key = (fam, name)
        expected = as_bool(r["expected_match"])
        expects[name].add(expected)
        confs[name].add(strata_of(r))
        pipe[key].add(expected, as_bool(r["pipeline_matched"]))
        if as_bool(r.get("pair_evaluated", "false")):
            pair[key].add(expected, as_bool(r["pair_matched"]))

    out.append("## Por transformação\n")
    out.append("`acerto` é a fração de amostras cujo veredito bateu com o rótulo. "
               "A coluna `par a par` usa a mesma amostra com a referência correta "
               "entregue de mão beijada; a diferença entre as duas colunas é o custo "
               "do pré-filtro.\n")
    out.append("| família | transformação | rótulo | estrato | n | acerto pipeline [IC 95%] "
               "| acerto par a par [IC 95%] |")
    out.append("| --- | --- | --- | --- | ---: | --- | --- |")
    for fam, name in sorted(pipe):
        c = pipe[(fam, name)]
        p = pair[(fam, name)]
        label = "match" if True in expects[name] else "rejeita"
        if expects[name] == {True, False}:
            label = "misto"
        stratum = "alta" if confs[name] == {HIGH} else (
            "limítrofe" if confs[name] == {BORDERLINE} else "misto")
        out.append(
            f"| {fam} | {name} | {label} | {stratum} | {c.total} "
            f"| {fmt_rate(c.tp + c.tn, c.total)} "
            f"| {fmt_rate(p.tp + p.tn, p.total) if p.total else '—'} |"
        )
    out.append("")
    out.append("> Com cerca de 1.000 amostras por transformação a meia-largura do "
               "intervalo de 95% fica em no máximo ~3 pontos percentuais. Diferenças "
               "menores que isso entre famílias não devem ser interpretadas.")
    out.append("")


def section_controls(out: list[str], rows: list[dict]) -> None:
    controls = [r for r in rows if as_bool(r.get("is_control", "false"))]
    samples = [r for r in rows if not as_bool(r.get("is_control", "false"))]
    matched = sum(1 for r in controls if as_bool(r["pipeline_certified"]))

    # "Returned somebody else's certificate" splits into two very different
    # events, and conflating them inflates the error count badly.
    #
    # A different_image sample carries the peer base's bytes untouched, and the
    # peer is itself certified, so SHA-256 returns the peer's own certificate.
    # That is the truthful answer, not a misattribution: the query really is
    # that image. It counts as a correct true negative against the base under
    # test, and it is the overwhelming majority of "other" verdicts.
    #
    # The number chapter 7 should report is the other one: a sample attributed
    # to a different certificate by *visual matching*, where the system claimed
    # a likeness it should not have.
    by_sha = [r for r in samples
              if r.get("cert_target") == "other" and r.get("decided_stage") == "sha256"]
    by_visual = [r for r in samples
                 if r.get("cert_target") == "other"
                 and r.get("decided_stage") != "sha256"]
    visual_candidates = [r for r in samples if r.get("decided_stage") != "sha256"]

    out.append("## Controles negativos\n")
    out.append("| controle | n | eventos | taxa [IC 95%] |")
    out.append("| --- | ---: | ---: | --- |")
    out.append(f"| Imagem nunca certificada casou com algum certificado | {len(controls)} "
               f"| {matched} | {fmt_rate(matched, len(controls))} |")
    out.append(f"| Atribuída a outro certificado por correspondência visual "
               f"| {len(visual_candidates)} | {len(by_visual)} "
               f"| {fmt_rate(len(by_visual), len(visual_candidates))} |")
    out.append(f"| Resolvida pelo SHA-256 para o certificado do próprio par "
               f"(correto por construção) | {len(samples)} | {len(by_sha)} "
               f"| {fmt_rate(len(by_sha), len(samples))} |")
    out.append("")

    if len(controls) and matched == 0:
        _, _, hi = wilson(0, len(controls))
        out.append(f"> Zero eventos em {len(controls)} tentativas tem limite superior de "
                   f"**{hi * 100:.2f}%** a 95%. Reporte esse limite, não \"nunca erra\".")
        out.append("")

    if by_visual:
        per_base = Counter(r["base_id"] for r in by_visual)
        out.append("Atribuições visuais a outro certificado, por base de origem. "
                   "Uma única base concentrando muitas é sinal de conteúdo duplicado "
                   "no dataset, não de erro do sistema — verifique antes de reportar "
                   "como falso positivo:\n")
        out.append("| base | ocorrências |")
        out.append("| --- | ---: |")
        for bid, n in per_base.most_common(10):
            out.append(f"| {bid} | {n} |")
        out.append("")

    out.append(f"> O controle `{CONTROL_FAMILY}` consulta a imagem de uma base com o "
               "certificado dela removido do banco, então o SHA-256 não resolve e a "
               "correspondência visual roda contra todos os outros certificados sem "
               "resposta correta disponível. O `different_image` do manifest não testa "
               "isso: ele devolve os bytes do par intactos, e o par é certificado, "
               "então o SHA-256 decide antes de qualquer comparação visual.")
    out.append("")


def section_stages(out: list[str], rows: list[dict]) -> None:
    tally = Counter(r.get("decided_stage", "") for r in rows)
    total = sum(tally.values())
    out.append("## Estágio que decidiu\n")
    out.append("| estágio | n | fração |")
    out.append("| --- | ---: | ---: |")
    for stage, n in tally.most_common():
        out.append(f"| `{stage or '(vazio)'}` | {n} | {n / total:.3f} |")
    out.append("")


def section_prefilter(out: list[str], rows: list[dict]) -> None:
    samples = [r for r in rows
               if not as_bool(r.get("is_control", "false"))
               and as_bool(r["expected_match"])]
    if not samples:
        return

    present = [r for r in samples if as_bool(r.get("prefilter_has_correct", "false"))]
    ranks = [as_int(r.get("prefilter_correct_rank")) for r in present]
    ranks = sorted(x for x in ranks if x is not None and x >= 0)
    in_window = sum(1 for x in ranks if x < 64)
    first = sum(1 for x in ranks if x == 0)

    out.append("## Pré-filtro\n")
    out.append("Medido apenas nas amostras com rótulo positivo, onde existe um "
               "certificado correto para encontrar.\n")
    out.append("| medida | n | eventos | taxa [IC 95%] |")
    out.append("| --- | ---: | ---: | --- |")
    out.append(f"| Certificado correto chegou à lista de candidatos | {len(samples)} "
               f"| {len(present)} | {fmt_rate(len(present), len(samples))} |")
    out.append(f"| Certificado correto dentro da janela top-64 | {len(samples)} "
               f"| {in_window} | {fmt_rate(in_window, len(samples))} |")
    out.append(f"| Certificado correto em primeiro lugar | {len(samples)} "
               f"| {first} | {fmt_rate(first, len(samples))} |")
    out.append("")

    cands = sorted(x for x in (as_float(r.get("prefilter_candidates")) for r in samples)
                   if x is not None)
    hamm = sorted(x for x in (as_float(r.get("phash_hamming")) for r in samples)
                  if x is not None and x >= 0)
    if cands:
        out.append("| distribuição | mediana | p95 | máx |")
        out.append("| --- | ---: | ---: | ---: |")
        out.append(f"| Candidatos avaliados por consulta | {percentile(cands, 0.5):.0f} "
                   f"| {percentile(cands, 0.95):.0f} | {cands[-1]:.0f} |")
        if hamm:
            out.append(f"| Distância pHash até o certificado correto | "
                       f"{percentile(hamm, 0.5):.0f} | {percentile(hamm, 0.95):.0f} "
                       f"| {hamm[-1]:.0f} |")
        out.append("")

    # Two distinct failure modes hide behind "the correct certificate was not a
    # candidate", and they point at different parts of the design.
    #
    # The band probe only retrieves a certificate when at least one of the 32
    # pHash bytes matches exactly, so a certificate can sit inside the Hamming
    # threshold and still never be looked at. That is a recall limit of the LSH
    # index: neither a wider top-K nor a looser Hamming threshold recovers it.
    #
    # Losing the certificate because its distance exceeds the threshold is the
    # other mode, and there the threshold is the knob.
    lost_rows = [r for r in samples
                 if not as_bool(r.get("prefilter_has_correct", "false"))]
    lost_band = lost_hamming = lost_unknown = 0
    for r in lost_rows:
        h = as_int(r.get("phash_hamming"))
        if h is None or h < 0:
            lost_unknown += 1
        elif h > 96:
            lost_hamming += 1
        else:
            lost_band += 1

    if lost_rows:
        out.append("Causa da perda, quando o certificado correto não chegou aos "
                   "candidatos:\n")
        out.append("| causa | n | fração das positivas [IC 95%] |")
        out.append("| --- | ---: | --- |")
        out.append(f"| Distância pHash acima de 96 | {lost_hamming} "
                   f"| {fmt_rate(lost_hamming, len(samples))} |")
        out.append(f"| Dentro de 96, mas sem colisão de banda | {lost_band} "
                   f"| {fmt_rate(lost_band, len(samples))} |")
        if lost_unknown:
            out.append(f"| pHash indisponível | {lost_unknown} "
                       f"| {fmt_rate(lost_unknown, len(samples))} |")
        out.append("")
        out.append("> A segunda linha é a que mais importa na discussão: a sonda por "
                   "bandas só encontra um certificado quando pelo menos um dos 32 bytes "
                   "do pHash coincide exatamente. Um certificado pode estar dentro do "
                   "limiar de Hamming e ainda assim nunca ser avaliado — nem um top-K "
                   "maior nem um limiar mais folgado recuperam esses casos, só um "
                   "esquema de bandas diferente.")
        out.append("")

        # The rows that matter most: the pairwise matcher would have matched, so
        # the only thing between the system and a correct answer was the search.
        recoverable = [r for r in lost_rows if as_bool(r.get("pair_matched", "false"))]
        by_transform = Counter(r.get("transform", "") for r in lost_rows)
        by_recoverable = Counter(r.get("transform", "") for r in recoverable)
        out.append(f"Em **{len(lost_rows)}** amostras positivas o certificado correto "
                   f"nunca chegou à correspondência visual. Dessas, **{len(recoverable)}** "
                   "teriam casado par a par: são falsos negativos atribuíveis "
                   "inteiramente ao pré-filtro.\n")
        out.append("| transformação | perdidas | das quais casariam par a par |")
        out.append("| --- | ---: | ---: |")
        for name, n in by_transform.most_common(15):
            out.append(f"| {name} | {n} | {by_recoverable.get(name, 0)} |")
        out.append("")


LATENCY_STAGES = [
    ("ms_sha256", "SHA-256"),
    ("ms_exact_lookup", "Consulta exata"),
    ("ms_phash", "pHash (4 rotações)"),
    ("ms_orb_extract", "Extração ORB"),
    ("ms_prefilter", "Pré-filtro LSH"),
    ("ms_match", "Correspondência visual"),
    ("ms_total", "Verificação completa"),
]


def section_latency(out: list[str], rows: list[dict]) -> None:
    out.append("## Latência\n")
    out.append("| estágio | n | mediana (ms) | p95 (ms) | máx (ms) |")
    out.append("| --- | ---: | ---: | ---: | ---: |")
    for col, label in LATENCY_STAGES:
        vals = sorted(x for x in (as_float(r.get(col)) for r in rows) if x is not None)
        if not vals:
            continue
        out.append(f"| {label} | {len(vals)} | {percentile(vals, 0.5):.1f} "
                   f"| {percentile(vals, 0.95):.1f} | {vals[-1]:.1f} |")
    out.append("")

    # The decisive split: a query that finds its match early stops, while one
    # that finds nothing pays a full match against every candidate.
    matched = [r for r in rows if as_bool(r["pipeline_certified"])]
    unmatched = [r for r in rows if not as_bool(r["pipeline_certified"])]
    out.append("| veredito | n | mediana total (ms) | p95 total (ms) |")
    out.append("| --- | ---: | ---: | ---: |")
    for label, subset in (("Casou", matched), ("Não casou", unmatched)):
        vals = sorted(x for x in (as_float(r.get("ms_total")) for r in subset)
                      if x is not None)
        if not vals:
            continue
        out.append(f"| {label} | {len(vals)} | {percentile(vals, 0.5):.1f} "
                   f"| {percentile(vals, 0.95):.1f} |")
    out.append("")
    out.append("> Reporte a latência apenas da rodada em uma thread "
               "(`TCC_WORKERS=1`), informando o hardware. Números colhidos na rodada "
               "paralela medem disputa de CPU, não o custo do pipeline.")
    out.append("")


# --------------------------------------------------------------------------- #

def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("csv", help="per-sample CSV written by TCC_OUT")
    ap.add_argument("--out", help="write Markdown here instead of stdout")
    ap.add_argument("--exclude", help="base ids to drop: a file with one id per line, "
                                      "or a comma-separated list")
    args = ap.parse_args()

    excluded = read_exclude(args.exclude)
    rows, skipped = load(args.csv, excluded)
    if not rows:
        sys.exit(f"{args.csv}: no rows left to summarise")

    out: list[str] = ["# Resumo do benchmark Aletheia\n"]
    section_scenario(out, args.csv, rows, skipped, excluded)

    section_confusion(
        out, "Pipeline completo",
        "Veredito do caminho completo de `verify.go`: SHA-256, pHash nas 4 rotações, "
        "pré-filtro LSH por bandas com Hamming ≤ 96, janela top-64 e correspondência "
        "visual. Uma amostra conta como acerto apenas quando o certificado devolvido é "
        "o da base correta. As linhas excluem os controles "
        f"`{CONTROL_FAMILY}`, reportados em seção própria.",
        confusion_by_stratum(rows, "pipeline_matched"))

    section_confusion(
        out, "Par a par",
        "Veredito do matcher isolado: ORB, RANSAC, resíduo de cor e cobertura contra a "
        "base correta, sem busca. É o teto que o pipeline persegue.",
        confusion_by_stratum(rows, "pair_matched", require_evaluated="pair_evaluated"))

    section_families(out, rows)
    section_controls(out, rows)
    section_prefilter(out, rows)
    section_stages(out, rows)
    section_latency(out, rows)

    text = "\n".join(out)
    if args.out:
        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write(text)
        print(f"wrote {args.out}")
    else:
        print(text)


if __name__ == "__main__":
    main()
