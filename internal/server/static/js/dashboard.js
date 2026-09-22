// ── dashboard page ─────────────────────────────────────

async function pollStats() {
  let s, m;
  try {
    const [a, b] = await Promise.all([
      fetch("/api/v1/stats", { cache: "no-store" }),
      fetch("/api/v1/mode",  { cache: "no-store" }),
    ]);
    s = await a.json();
    m = await b.json();
  } catch {
    return;
  }

  // CPU
  $("cpu-val").textContent = s.cpu_percent.toFixed(1);
  $("cpu-val").className = "value " + sev(s.cpu_percent);
  $("cpu-cores").textContent = `${s.cpu_cores} threads`;
  drawSpark($("cpu-spark"), s.cpu_history, 100);

  // Memory
  $("mem-val").textContent = s.mem_percent.toFixed(1);
  $("mem-val").className = "value " + sev(s.mem_percent);
  $("mem-meta").textContent = `${s.mem_used_mb} / ${s.mem_total_mb} MB`;
  drawSpark($("mem-spark"), s.mem_history, 100);

  // Load average — normalised against logical core count so a 24-thread
  // box doesn't look alarming at load 3.
  const ratio = s.load_ratio;
  $("load-val").textContent = s.load_avg_1.toFixed(2);
  $("load-val").className = "value " + (ratio > 1 ? "bad" : ratio > 0.7 ? "warn" : "");
  $("load-unit").textContent = `/ ${s.cpu_cores}`;
  $("load-meta").textContent =
    `${(ratio * 100).toFixed(0)}% · 5m ${s.load_avg_5.toFixed(2)} · 15m ${s.load_avg_15.toFixed(2)}`;
  drawSpark($("load-spark"), s.load_history, 2.0);

  // Temperature
  if (s.cpu_temp > 0) {
    $("temp-val").textContent = s.cpu_temp.toFixed(0);
    $("temp-val").className = "value " +
      (s.cpu_temp >= 85 ? "bad" : s.cpu_temp >= 70 ? "warn" : "");
  } else {
    $("temp-val").textContent = "—";
    $("temp-val").className = "value";
  }
  $("temp-meta").textContent = (s.temps || []).length
    ? `${s.temps.length} sensors` : "no sensors";

  const trows = (s.temps || []).slice(0, 6).map(t => ({
    k: t.key.replace(/^(coretemp|k10temp|cpu|acpitz)[_\-]?/i, "").replace(/_/g, " ") || t.key,
    v: `${t.temp.toFixed(0)}°C`,
    cls: t.temp >= 85 ? "bad" : t.temp >= 70 ? "warn" : "",
  }));
  renderRows($("temp-list"), trows);

  // Storage
  const root = s.disk_root || {};
  const rp = root.percent || 0;
  $("disk-val").textContent = rp.toFixed(1);
  $("disk-val").className = "value " + sev(rp);
  $("disk-meta").textContent = root.total_gb
    ? `${root.used_gb} / ${root.total_gb} GB` : "—";

  const drows = [];
  if (root.mount) {
    drows.push({ k: `${root.mount} (${root.fstype})`, v: `${rp.toFixed(0)}%`, cls: sev(rp) });
  }
  (s.disks || []).slice(0, 4).forEach(d => {
    drows.push({ k: `${d.mount} (${d.fstype})`, v: `${d.percent.toFixed(0)}%`, cls: sev(d.percent) });
  });
  renderRows($("disk-list"), drows);

  // Go runtime
  $("bot-val").textContent = s.goroutines;
  $("bot-heap").textContent = `${s.go_heap_mb} MB`;
  $("bot-uptime").textContent = fmtDuration(s.bot_uptime_seconds);

  // Header + footer
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