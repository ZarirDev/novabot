// ── shared helpers and router ──────────────────────────
// Loaded first. Every other script in this app depends on the
// symbols defined here being in the global script scope.

const PAGES = ["dashboard", "music", "settings"];
let currentPage = "dashboard";

// Alias for document.getElementById.
const $ = id => document.getElementById(id);

// Compact human duration. Used for host uptime and bot uptime.
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

// m:ss — used for media timestamps in the music player.
function fmtTime(sec) {
  sec = Math.max(0, Math.floor(sec || 0));
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// Severity class for a 0-100 percentage value.
function sev(v) {
  if (v >= 90) return "bad";
  if (v >= 70) return "warn";
  return "";
}

// Draw a sparkline into an SVG element. Uses a normalised 100x100
// viewBox; CSS preserveAspectRatio="none" stretches it to the target
// size, and non-scaling-stroke keeps the line crisp at any width.
function drawSpark(svg, data, max) {
  if (!svg) return;
  if (!data || data.length < 2) { svg.innerHTML = ""; return; }

  const W = 100, H = 100;
  const step = W / (data.length - 1);
  const scale = max > 0 ? H / max : 0;

  let line = "";
  let area = `M0,${H}`;
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

// Render key-value rows into a list container. Each row: {k, v, cls}.
function renderRows(el, rows) {
  if (!el) return;
  if (!rows || rows.length === 0) {
    el.innerHTML = `<div class="list-row"><span class="k">no data</span></div>`;
    return;
  }
  el.innerHTML = rows.map(r =>
    `<div class="list-row">` +
      `<span class="k" title="${escapeHTML(r.k)}">${escapeHTML(r.k)}</span>` +
      `<span class="v ${r.cls || ""}">${escapeHTML(r.v)}</span>` +
    `</div>`
  ).join("");
}

// HTML entity escaper for values injected into templates.
function escapeHTML(s) {
  return String(s || "").replace(/[&<>"']/g, c => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

// ── router ──────────────────────────────────────────────
// Called once at boot and again on every hash change. Toggles page
// visibility and kicks off the initial load for the incoming page.
//
// The function references below (pollStats, musicStatus, load*) are
// resolved at call time, not parse time — so this file can be loaded
// before the page-specific scripts have defined them. `route()` is
// only invoked from main.js, which loads last.
function route() {
  const hash = location.hash.replace(/^#\/?/, "") || "dashboard";
  const page = PAGES.includes(hash) ? hash : "dashboard";
  currentPage = page;

  PAGES.forEach(p => {
    const el = document.getElementById("page-" + p);
    if (el) el.classList.toggle("hidden", p !== page);
  });
  document.querySelectorAll(".nav-item[data-page]").forEach(el => {
    el.classList.toggle("active", el.dataset.page === page);
  });

  if (page === "dashboard") {
    pollStats();
  } else if (page === "music") {
    musicStatus();
  } else if (page === "settings") {
      loadSettings();
      loadQuality();
      loadVolume();
      loadDevices();
      loadLogging();
      pollAudioStatus();
  }
}