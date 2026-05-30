"use strict";

// PT-BR labels for pipeline stages.
const STAGE_LABELS = {
  sha256: "Hash SHA-256",
  duplicate_check: "Verificação de duplicata",
  phash256: "Hash perceptual (pHash)",
  phash_variants: "Variantes do pHash (rotações)",
  orb_extract: "Extração de features ORB",
  feature_commitment: "Compromisso de features (commitment)",
  chain_anchor: "Ancoragem em blockchain",
  blob_put: "Armazenamento da imagem (S3)",
  db_save: "Persistência no banco (Postgres)",
  exact_lookup: "Busca exata por hash",
  lsh_prefilter: "Pré-filtro LSH (candidatos)",
  candidate_matching: "Comparação de candidatos",
};

// PT-BR labels for stage attributes.
const ATTR_LABELS = {
  content_hash: "Hash SHA-256",
  size_bytes: "Tamanho (bytes)",
  is_duplicate: "Já certificado?",
  is_match: "Casou exatamente?",
  phash: "pHash (hex)",
  has_phash: "pHash extraído?",
  keypoints: "Keypoints ORB",
  descriptors: "Descritores ORB",
  jpeg_bytes: "JPEG normalizado (bytes)",
  blob_key: "Chave no blob store",
  commitment: "Commitment (hex)",
  tx_hash: "Hash da transação",
  block_number: "Bloco",
  cert_id: "ID do certificado",
  variants: "Variantes",
  candidates: "Candidatos encontrados",
  reason: "Motivo",
};

const VERDICT_LABELS = {
  certified: "Certificado",
  duplicate: "Duplicado",
  match: "Verificado",
  no_match: "Não encontrado",
  error: "Erro",
};

const PIPELINE_LABELS = { certify: "Certificação", verify: "Verificação" };

const el = (id) => document.getElementById(id);

// Active trace state: ordered list of top-level stages keyed by name.
let active = { id: null, pipeline: "", stages: new Map(), order: [] };

function resetActive(id) {
  active = { id, pipeline: "", stages: new Map(), order: [] };
  el("pipeline").innerHTML = "";
  el("live-badge").hidden = true;
  el("live-total").textContent = "";
  el("live-title").textContent = "Processando…";
  el("live-hint").hidden = true;
}

function upsertStage(stage) {
  if (!active.stages.has(stage.name)) active.order.push(stage.name);
  active.stages.set(stage.name, stage);
  renderPipeline();
}

function fmtMs(ms) {
  if (ms == null) return "";
  if (ms < 1) return ms.toFixed(2) + " ms";
  return ms.toFixed(1) + " ms";
}

function fmtVal(key, value) {
  if (typeof value === "boolean") return value ? "sim" : "não";
  if (typeof value === "string" && value.length > 24 &&
      (key === "content_hash" || key === "commitment" || key === "phash" || key === "tx_hash")) {
    return value.slice(0, 12) + "…" + value.slice(-8);
  }
  return String(value);
}

function attrLabel(key) {
  return ATTR_LABELS[key] || key;
}

function renderAttrs(stage) {
  const visible = (stage.attrs || []).filter((a) => a.key !== "reason" || stage.status !== "ok");
  if (stage.error) visible.push({ key: "Erro", value: stage.error, _fail: true });
  if (!visible.length) return "";
  const rows = visible
    .map((a) => {
      const cls = a._fail ? "v fail" : "v";
      const k = a._fail ? a.key : attrLabel(a.key);
      return `<span class="k">${k}</span><span class="${cls}">${fmtVal(a.key, a.value)}</span>`;
    })
    .join("");
  return `<div class="attrs">${rows}</div>`;
}

// Pull a numeric attr value out of a candidate stage.
function attr(stage, key) {
  const a = (stage.attrs || []).find((x) => x.key === key);
  return a ? a.value : undefined;
}

function renderCandidates(stage) {
  const kids = stage.children || [];
  if (!kids.length) return "";
  const rows = kids
    .map((c, i) => {
      const matched = attr(c, "matched") === true;
      const hamming = attr(c, "hamming");
      const inliers = attr(c, "inliers");
      const minIn = attr(c, "min_inliers");
      const cmean = attr(c, "color_mean");
      const cmeanMax = attr(c, "max_color_mean");
      const cmax = attr(c, "color_max");
      const cmaxMax = attr(c, "max_cell_dist");
      const skipped = c.status === "skipped";
      const reason = attr(c, "reason");
      const blobKey = attr(c, "blob_key");
      const num = (v, d = 1) => (v == null ? "—" : Number(v).toFixed(d));
      const over = (v, lim) => (v != null && lim != null && v > lim ? " class=\"over\"" : "");
      const pill = skipped
        ? `<span class="verdict-pill no">pulado</span>`
        : matched
        ? `<span class="verdict-pill yes">match</span>`
        : `<span class="verdict-pill no">não match</span>`;
      const thumb = blobKey
        ? `<img class="cand-thumb" loading="lazy" alt="candidato #${i + 1}" src="/observability/blob/${encodeURIComponent(blobKey)}" onerror="this.style.display='none'">`
        : "";
      return `<tr class="${matched ? "matched" : ""}">
        <td><div class="cand-cell">${thumb}<span>#${i + 1} ${pill}</span></div></td>
        <td>${hamming == null ? "—" : hamming}</td>
        <td${over(inliers != null ? minIn - inliers : 0, 0)}>${inliers == null ? "—" : inliers}${minIn != null ? " / " + minIn : ""}</td>
        <td${over(cmean, cmeanMax)}>${num(cmean, 2)}${cmeanMax != null ? " / " + cmeanMax : ""}</td>
        <td${over(cmax, cmaxMax)}>${num(cmax, 1)}${cmaxMax != null ? " / " + cmaxMax : ""}</td>
      </tr>
      ${reason && !matched ? `<tr><td colspan="5" class="cand-row-reason">↳ ${reason}</td></tr>` : ""}`;
    })
    .join("");
  return `<table class="cand-table">
    <thead><tr>
      <th>Candidato</th><th>Hamming</th><th>Inliers / mín</th>
      <th>Cor média / máx</th><th>Cor célula / máx</th>
    </tr></thead>
    <tbody>${rows}</tbody>
  </table>`;
}

function renderPipeline() {
  const ol = el("pipeline");
  ol.innerHTML = active.order
    .map((name) => {
      const s = active.stages.get(name);
      const label = STAGE_LABELS[name] || name;
      const body =
        name === "candidate_matching" ? renderCandidates(s) : renderAttrs(s);
      const dur = s.status === "ok" || s.status === "error" || s.status === "skipped" ? fmtMs(s.durMs) : "…";
      return `<li class="stage ${s.status === "ok" && s.durMs === 0 ? "running" : s.status}">
        <span class="node"></span>
        <div class="stage-row">
          <span class="stage-name">${label}</span>
          <span class="stage-dur">${dur}</span>
        </div>
        ${body}
      </li>`;
    })
    .join("");
}

function finishTrace(ev) {
  const v = ev.verdict || {};
  const badge = el("live-badge");
  badge.hidden = false;
  badge.className = "badge " + (v.outcome || "");
  badge.textContent = VERDICT_LABELS[v.outcome] || v.outcome || "—";
  el("live-total").textContent = "total " + fmtMs(ev.totalMs);
  const pl = PIPELINE_LABELS[ev.pipeline] || ev.pipeline || "";
  el("live-title").textContent = pl ? pl + " concluída" : "Concluído";
  loadHistory();
}

function setPipeline(pipeline) {
  if (!pipeline) return;
  active.pipeline = pipeline;
  el("live-title").textContent = (PIPELINE_LABELS[pipeline] || pipeline) + " em andamento…";
}

function handleEvent(ev) {
  switch (ev.type) {
    case "trace_start":
      resetActive(ev.traceId);
      break;
    case "stage_start":
    case "stage_update":
    case "stage_end":
      if (ev.traceId !== active.id) resetActive(ev.traceId);
      if (ev.pipeline) setPipeline(ev.pipeline);
      if (ev.stage) upsertStage(ev.stage);
      break;
    case "trace_end":
      if (ev.pipeline) setPipeline(ev.pipeline);
      finishTrace(ev);
      break;
  }
}

// ---- History ----

let selectedHistory = null;

async function loadHistory() {
  try {
    const res = await fetch("/observability/traces");
    const traces = await res.json();
    renderHistory(traces || []);
  } catch (e) {
    /* ignore */
  }
}

function renderHistory(traces) {
  const ul = el("history");
  ul.innerHTML = traces
    .slice()
    .reverse()
    .map((t) => {
      const v = t.verdict || {};
      const pl = PIPELINE_LABELS[t.pipeline] || t.pipeline || "—";
      const when = t.startedAt ? new Date(t.startedAt).toLocaleTimeString("pt-BR") : "";
      return `<li class="history-item ${t.id === selectedHistory ? "active" : ""}" data-id="${t.id}">
        <div class="hi-top">
          <span class="hi-pipe">${pl}</span>
          <span class="badge ${v.outcome || ""}">${VERDICT_LABELS[v.outcome] || v.outcome || "—"}</span>
        </div>
        <div class="hi-meta"><span>${when}</span><span>${fmtMs(t.totalMs)}</span></div>
      </li>`;
    })
    .join("");
  ul.querySelectorAll(".history-item").forEach((li) => {
    li.addEventListener("click", () => showTrace(li.dataset.id));
  });
}

async function showTrace(id) {
  try {
    const res = await fetch("/observability/traces/" + id);
    if (!res.ok) return;
    const t = await res.json();
    selectedHistory = id;
    resetActive(t.id);
    setPipeline(t.pipeline);
    (t.stages || []).forEach(upsertStage);
    finishTrace({ verdict: t.verdict, totalMs: t.totalMs, pipeline: t.pipeline });
    document.querySelectorAll(".history-item").forEach((li) =>
      li.classList.toggle("active", li.dataset.id === id)
    );
  } catch (e) {
    /* ignore */
  }
}

// ---- Connection ----

function connect() {
  const src = new EventSource("/observability/events");
  src.onopen = () => {
    el("conn-dot").className = "dot on";
    el("conn-label").textContent = "ao vivo";
  };
  src.onerror = () => {
    el("conn-dot").className = "dot off";
    el("conn-label").textContent = "reconectando…";
  };
  src.onmessage = (m) => {
    try {
      handleEvent(JSON.parse(m.data));
    } catch (e) {
      /* ignore malformed */
    }
  };
}

connect();
loadHistory();
