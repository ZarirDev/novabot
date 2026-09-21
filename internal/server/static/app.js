// ── router ──────────────────────────────────────────────
const PAGES = ["dashboard", "music", "settings"];
let currentPage = "dashboard";

function route() {
  const hash = location.hash.replace(/^#\/?/, "") || "dashboard";
  const page = PAGES.includes(hash) ? hash : "dashboard";
  currentPage = page;

  PAGES.forEach(p => {
    document.getElementById("page-" + p).classList.toggle("hidden", p !== page);
  });
  document.querySelectorAll(".nav-item[data-page]").forEach(el => {
    el.classList.toggle("active", el.dataset.page === page);
  });

  if (page === "settings") loadSettings();
  if (page === "music") musicStatus();
}

window.addEventListener("hashchange", route);
route();

// ── shared helpers ──────────────────────────────────────
const $ = id => document.getElementById(id);

function fmtDuration(sec) {
  sec = Math.floor(sec);
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

function fmtTime(sec) {
  sec = Math.max(0, Math.floor(sec || 0));
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function sev(v) {
  if (v >= 90) return "bad";
  if (v >= 70) return "warn";
  return "";
}

function drawSpark(svg, data, max) {
  if (!svg) return;
  if (!data || data.length < 2) { svg.innerHTML = ""; return; }
  const W = 100, H = 100;
  const step = W / (data.length - 1);
  const scale = max > 0 ? H / max : 0;
  let line = "", area = `M0,${H}`;
  for (let i = 0; i < data.length; i++) {
    const x = (i * step).toFixed(2);
    const y = (H - Math.min(data[i], max) * scale).toFixed(2);
    line += (i === 0 ? `M${x},${y}` : ` L${x},${y}`);
    area += ` L${x},${y}`;
  }
  area += ` L${W},${H} Z`;
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.innerHTML = `<path class="fill" d="${area}"/><path class="line" d="${line}"/>`;
}

function renderRows(el, rows) {
  if (!el) return;
  if (!rows || rows.length === 0) {
    el.innerHTML = `<div class="list-row"><span class="k">no data</span></div>`;
    return;
  }
  el.innerHTML = rows.map(r =>
    `<div class="list-row"><span class="k" title="${r.k}">${r.k}</span><span class="v ${r.cls || ""}">${r.v}</span></div>`
  ).join("");
}

function escapeHTML(s) {
  return String(s || "").replace(/[&<>"']/g, c => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

// ── dashboard ───────────────────────────────────────────
async function pollStats() {
  if (currentPage !== "dashboard") return;
  let s, m;
  try {
    const [a, b] = await Promise.all([
      fetch("/api/v1/stats", { cache: "no-store" }),
      fetch("/api/v1/mode",  { cache: "no-store" }),
    ]);
    s = await a.json();
    m = await b.json();
  } catch { return; }

  $("cpu-val").textContent = s.cpu_percent.toFixed(1);
  $("cpu-val").className = "value " + sev(s.cpu_percent);
  $("cpu-cores").textContent = `${s.cpu_cores} threads`;
  drawSpark($("cpu-spark"), s.cpu_history, 100);

  $("mem-val").textContent = s.mem_percent.toFixed(1);
  $("mem-val").className = "value " + sev(s.mem_percent);
  $("mem-meta").textContent = `${s.mem_used_mb} / ${s.mem_total_mb} MB`;
  drawSpark($("mem-spark"), s.mem_history, 100);

  const ratio = s.load_ratio;
  $("load-val").textContent = s.load_avg_1.toFixed(2);
  $("load-val").className = "value " + (ratio > 1 ? "bad" : ratio > 0.7 ? "warn" : "");
  $("load-unit").textContent = `/ ${s.cpu_cores}`;
  $("load-meta").textContent = `${(ratio*100).toFixed(0)}% · 5m ${s.load_avg_5.toFixed(2)} · 15m ${s.load_avg_15.toFixed(2)}`;
  drawSpark($("load-spark"), s.load_history, 2.0);

  if (s.cpu_temp > 0) {
    $("temp-val").textContent = s.cpu_temp.toFixed(0);
    $("temp-val").className = "value " + (s.cpu_temp >= 85 ? "bad" : s.cpu_temp >= 70 ? "warn" : "");
  }
  $("temp-meta").textContent = (s.temps || []).length ? `${s.temps.length} sensors` : "no sensors";
  const trows = (s.temps || []).slice(0, 6).map(t => ({
    k: t.key.replace(/^(coretemp|k10temp|cpu|acpitz)[_\-]?/i, "").replace(/_/g, " ") || t.key,
    v: `${t.temp.toFixed(0)}°C`,
    cls: t.temp >= 85 ? "bad" : t.temp >= 70 ? "warn" : "",
  }));
  renderRows($("temp-list"), trows);

  const root = s.disk_root || {};
  const rp = root.percent || 0;
  $("disk-val").textContent = rp.toFixed(1);
  $("disk-val").className = "value " + sev(rp);
  $("disk-meta").textContent = root.total_gb ? `${root.used_gb} / ${root.total_gb} GB` : "—";
  const drows = [];
  if (root.mount) drows.push({ k: `${root.mount} (${root.fstype})`, v: `${rp.toFixed(0)}%`, cls: sev(rp) });
  (s.disks || []).slice(0, 4).forEach(d => drows.push({ k: `${d.mount} (${d.fstype})`, v: `${d.percent.toFixed(0)}%`, cls: sev(d.percent) }));
  renderRows($("disk-list"), drows);

  $("bot-val").textContent = s.goroutines;
  $("bot-heap").textContent = `${s.go_heap_mb} MB`;
  $("bot-uptime").textContent = fmtDuration(s.bot_uptime_seconds);

  $("host").textContent = s.hostname || "—";
  $("platform").textContent = s.platform || "—";
  const modeEl = $("mode");
  modeEl.textContent = m.current_mode || "—";
  modeEl.setAttribute("data-mode", m.current_mode);
  $("side-mode").textContent = m.current_mode || "—";
  $("foot-host").textContent = s.platform || "—";
  $("foot-up").textContent = `host up ${fmtDuration(s.uptime_seconds)}`;
  $("foot-go").textContent = `goroutines ${s.goroutines}`;
}

// ── music ───────────────────────────────────────────────

let seeking = false;
let currentDuration = 0;

async function musicStatus() {
  if (currentPage !== "music") return;
  let s;
  try {
    const r = await fetch("/api/v1/music/status", { cache: "no-store" });
    s = await r.json();
  } catch { return; }

  $("np-title").textContent  = s.title || "nothing playing";
  $("np-artist").textContent = s.artist || (s.idle ? "—" : "youtube");

  const pos = s.position || 0;
  const dur = s.duration || 0;
  currentDuration = dur;

  $("np-pos").textContent = fmtTime(pos);
  $("np-dur").textContent = fmtTime(dur);

  if (!seeking) {
    const seek = $("np-seek");
    seek.max = dur > 0 ? dur : 100;
    seek.value = pos;
  }

  $("icon-play").classList.toggle("hidden", !s.paused && !s.idle);
  $("icon-pause").classList.toggle("hidden", s.paused || s.idle);

  const vol = $("np-vol");
  if (document.activeElement !== vol) vol.value = s.volume || 0;
}

async function doSearch() {
  const q = $("search-input").value.trim();
  if (!q) return;

  const el = $("results");
  const t0 = performance.now();
  el.innerHTML = `<div class="empty">searching…</div>`;
  console.log(`[music] search start: "${q}"`);

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), 50000);

  try {
    const r = await fetch(
      `/api/v1/music/search?q=${encodeURIComponent(q)}&limit=12`,
      { signal: ctrl.signal, cache: "no-store" }
    );
    clearTimeout(timeout);

    const elapsed = Math.round(performance.now() - t0);
    console.log(`[music] search response: ${r.status} in ${elapsed}ms`);

    let data;
    const text = await r.text();
    try {
      data = text ? JSON.parse(text) : {};
    } catch (parseErr) {
      console.error(`[music] invalid JSON (${text.length} bytes):`, text.slice(0, 200));
      el.innerHTML = `<div class="empty">server sent invalid response (status ${r.status})</div>`;
      return;
    }

    if (!r.ok) {
      const msg = data.error || `HTTP ${r.status}`;
      console.error("[music] search failed:", msg);
      el.innerHTML = `<div class="empty">search failed: ${escapeHTML(msg)}</div>`;
      return;
    }

    const tracks = data.tracks || [];
    console.log(`[music] search returned ${tracks.length} tracks`);

    if (tracks.length === 0) {
      el.innerHTML = `<div class="empty">no results</div>`;
      return;
    }

    el.innerHTML = tracks.map((t, i) => `
      <div class="track" data-url="${encodeURIComponent(t.url)}">
        <span class="track-idx">${String(i + 1).padStart(2, "0")}</span>
        <div class="track-info">
          <div class="track-title">${escapeHTML(t.title)}</div>
          <div class="track-artist">${escapeHTML(t.artist || "")}</div>
        </div>
        <span class="track-dur">${fmtTime(t.duration)}</span>
        <button class="track-play" title="play">
          <svg viewBox="0 0 16 16"><path d="M4 2l9 6-9 6V2z" fill="currentColor"/></svg>
        </button>
      </div>
    `).join("");

    el.querySelectorAll(".track").forEach(node => {
      node.addEventListener("click", async () => {
        const url = decodeURIComponent(node.dataset.url);
        console.log(`[music] play request: ${url}`);
        try {
          const res = await fetch("/api/v1/music/play", {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ url }),
          });
          if (!res.ok) {
            const body = await res.text();
            console.error(`[music] play failed (${res.status}):`, body);
          }
        } catch (err) {
          console.error("[music] play network error:", err);
        }
        musicStatus();
      });
    });
  } catch (err) {
    clearTimeout(timeout);
    const elapsed = Math.round(performance.now() - t0);
    if (err.name === "AbortError") {
      console.error(`[music] search aborted after ${elapsed}ms`);
      el.innerHTML = `<div class="empty">search timed out after 50s</div>`;
    } else {
      console.error(`[music] search error after ${elapsed}ms:`, err);
      el.innerHTML = `<div class="empty">search error: ${escapeHTML(err.message || err)}</div>`;
    }
  }
}

// ── settings ────────────────────────────────────────────

let settingsCache = { pc_audio_enabled: true };

async function loadSettings() {
  try {
    const r = await fetch("/api/v1/settings", { cache: "no-store" });
    settingsCache = await r.json();

    const toggle = $("toggle-pc-audio");
    if (toggle) toggle.checked = !!settingsCache.pc_audio_enabled;

    const cmdEl = $("client-cmd");
    if (cmdEl) {
      cmdEl.textContent = `novabot-client --server ${location.hostname} --port 4000`;
    }
  } catch (err) {
    console.error("[settings] load failed:", err);
  }
}

async function saveSettings() {
  const enabled = $("toggle-pc-audio").checked;
  console.log("[settings] setting pc_audio_enabled =", enabled);
  try {
    const r = await fetch("/api/v1/settings", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ pc_audio_enabled: enabled }),
    });
    settingsCache = await r.json();
    console.log("[settings] server says:", settingsCache);
    // Reflect reality — server is the source of truth.
    const toggle = $("toggle-pc-audio");
    if (toggle) toggle.checked = !!settingsCache.pc_audio_enabled;
  } catch (err) {
    console.error("[settings] save failed:", err);
    $("toggle-pc-audio").checked = !enabled;
  }
}

async function pollAudioStatus() {
  if (currentPage !== "settings") return;
  let s;
  try {
    const r = await fetch("/api/v1/audio/status", { cache: "no-store" });
    s = await r.json();
  } catch { return; }

  const dot = $("audio-status-dot");
  const text = $("audio-status-text");
  const detail = $("audio-status-detail");
  if (!dot || !text || !detail) return;

  let state, label;
  if (!s.listening) {
    state = "idle"; label = "not active";
    detail.textContent = "bot is not in PC_AUDIO mode";
  } else if (s.connected) {
    state = "connected"; label = "streaming";
    detail.textContent =
      `${s.packets_total} packets · ${s.kbps.toFixed(0)} kbps (of ${s.expected_kbps}) · ` +
      `latency p50 ${s.latency_p50_ms.toFixed(1)}ms / p95 ${s.latency_p95_ms.toFixed(1)}ms`;
  } else {
    state = "disconnected"; label = "disconnected";
    const last = s.last_packet && s.last_packet !== "0001-01-01T00:00:00Z"
      ? new Date(s.last_packet) : null;
    detail.textContent = last
      ? `last packet ${Math.round((Date.now() - last.getTime()) / 1000)}s ago — is the client running?`
      : "no packets received yet — is the client running?";
  }

  dot.setAttribute("data-state", state);
  text.textContent = `${label} (${s.quality}, ${s.sample_rate} Hz, ${s.format})`;
}

// ── event wiring ────────────────────────────────────────

// search
$("search-input")?.addEventListener("keydown", e => { if (e.key === "Enter") doSearch(); });
$("search-btn")?.addEventListener("click", doSearch);

// transport
$("btn-play")?.addEventListener("click", async () => {
  await fetch("/api/v1/music/pause", { method: "POST" });
  musicStatus();
});
$("btn-next")?.addEventListener("click", async () => {
  await fetch("/api/v1/music/next", { method: "POST" });
  musicStatus();
});
$("btn-prev")?.addEventListener("click", async () => {
  await fetch("/api/v1/music/prev", { method: "POST" });
  musicStatus();
});

// seek — pause polling while dragging, commit on release
const seek = $("np-seek");
seek?.addEventListener("pointerdown", () => { seeking = true; });
seek?.addEventListener("input", () => {
  $("np-pos").textContent = fmtTime(seek.value);
});
seek?.addEventListener("change", async () => {
  const pos = parseFloat(seek.value);
  seeking = false;
  await fetch("/api/v1/music/seek", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ position: pos }),
  });
});

// volume
const vol = $("np-vol");
vol?.addEventListener("input", () => {
  fetch("/api/v1/music/volume", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ volume: parseFloat(vol.value) }),
  });
});

// settings toggle
$("toggle-pc-audio")?.addEventListener("change", saveSettings);

// ── poll loops ──────────────────────────────────────────
pollStats();
setInterval(pollStats, 1500);
setInterval(musicStatus, 1000);
setInterval(() => { if (currentPage === "settings") loadSettings(); }, 2000);
setInterval(pollAudioStatus, 1000);
musicStatus();
pollAudioStatus();