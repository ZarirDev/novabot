// ── settings page ───────────────────────────────────────

// state caches, refreshed on every poll
let settingsCache = { pc_audio_enabled: false };
let qualityCache  = null;
let volumeCache   = 100;
let devicesCache  = { sinks: [], sources: [] };

// ── PC audio toggle ─────────────────────────────────────

async function loadSettings() {
  try {
    const r = await fetch("/api/v1/settings", { cache: "no-store" });
    settingsCache = await r.json();

    const toggle = $("toggle-pc-audio");
    if (toggle) toggle.checked = !!settingsCache.pc_audio_enabled;

    const cmdEl = $("client-cmd");
    if (cmdEl) {
      cmdEl.textContent =
        `novabot-client --server ${location.hostname} --port 4000`;
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
    // Server is the source of truth — reflect whatever it returns.
    const toggle = $("toggle-pc-audio");
    if (toggle) toggle.checked = !!settingsCache.pc_audio_enabled;
  } catch (err) {
    console.error("[settings] save failed:", err);
    $("toggle-pc-audio").checked = !enabled;
  }
}

// ── stream status indicator ─────────────────────────────

async function pollAudioStatus() {
  let s;
  try {
    const r = await fetch("/api/v1/audio/status", { cache: "no-store" });
    s = await r.json();
  } catch {
    return;
  }

  const dot = $("audio-status-dot");
  const text = $("audio-status-text");
  const detail = $("audio-status-detail");
  if (!dot || !text || !detail) return;

  let state, label;
  if (!s.listening) {
    state = "idle";
    label = "not active";
    detail.textContent = "bot is not in PC_AUDIO mode";
  } else if (s.connected) {
    state = "connected";
    label = "streaming";
    detail.textContent =
      `${s.packets_total} packets · ${s.kbps.toFixed(0)} kbps (of ${s.expected_kbps}) · ` +
      `latency p50 ${s.latency_p50_ms.toFixed(1)}ms / p95 ${s.latency_p95_ms.toFixed(1)}ms`;
  } else {
    state = "disconnected";
    label = "disconnected";
    const last = s.last_packet && s.last_packet !== "0001-01-01T00:00:00Z"
      ? new Date(s.last_packet) : null;
    detail.textContent = last
      ? `last packet ${Math.round((Date.now() - last.getTime()) / 1000)}s ago — is the client running?`
      : "no packets received yet — is the client running?";
  }

  dot.setAttribute("data-state", state);
  text.textContent = `${label} (${s.quality}, ${s.sample_rate} Hz, ${s.format})`;
}

// ── audio quality picker ────────────────────────────────

async function loadQuality() {
  try {
    const r = await fetch("/api/v1/audio/quality", { cache: "no-store" });
    qualityCache = await r.json();
    renderQualityPicker();
  } catch (err) {
    console.error("[quality] load failed:", err);
  }
}

function renderQualityPicker() {
  const el = $("quality-picker");
  if (!el || !qualityCache) return;

  const current = qualityCache.current?.name;
  const presets = qualityCache.presets || [];

  el.innerHTML = presets.map(p => `
    <button class="quality-opt ${p.name === current ? "active" : ""}"
            data-name="${p.name}">
      <span class="q-name">${p.name}</span>
      <span class="q-desc">${p.description}</span>
    </button>
  `).join("");

  el.querySelectorAll(".quality-opt").forEach(btn => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.name;
      if (name === qualityCache.current?.name) return;
      console.log("[quality] switching to", name);
      btn.classList.add("loading");
      try {
        const r = await fetch("/api/v1/audio/quality", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ name }),
        });
        if (!r.ok) {
          console.error("[quality] switch failed:", await r.text());
        }
        await loadQuality();
      } catch (err) {
        console.error("[quality] network error:", err);
      } finally {
        btn.classList.remove("loading");
      }
    });
  });
}

// ── stream volume ───────────────────────────────────────

async function loadVolume() {
  try {
    const r = await fetch("/api/v1/audio/volume", { cache: "no-store" });
    const data = await r.json();
    volumeCache = data.percent ?? 100;
    updateVolumeUI();
  } catch { /* ignore */ }
}

function updateVolumeUI() {
  const slider = $("volume-slider");
  if (!slider) return;
  if (document.activeElement !== slider) slider.value = volumeCache;

  $("volume-pct").textContent = `${volumeCache}%`;
  const db = volumeCache <= 0
    ? "-∞"
    : (20 * Math.log10(volumeCache / 100)).toFixed(1);
  $("volume-db").textContent = `${db} dB`;
}

async function saveVolume(pct) {
  try {
    const r = await fetch("/api/v1/audio/volume", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ percent: pct }),
    });
    const data = await r.json();
    volumeCache = data.percent ?? pct;
    updateVolumeUI();
  } catch (err) {
    console.error("[volume] save failed:", err);
  }
}

// ── device selectors ────────────────────────────────────

async function loadDevices() {
  try {
    const r = await fetch("/api/v1/audio/devices", { cache: "no-store" });
    devicesCache = await r.json();
    renderDevices();
  } catch (err) {
    console.error("[devices] load failed:", err);
  }
}

function renderDevices() {
  renderDeviceSelect("device-sink",   devicesCache.sinks,   "sink");
  renderDeviceSelect("device-source", devicesCache.sources, "source");

  setDeviceHint("sink-hint",   devicesCache.sinks_error);
  setDeviceHint("source-hint", devicesCache.sources_error);
}

function setDeviceHint(id, errText) {
  const el = $(id);
  if (!el) return;
  if (errText) {
    el.textContent = errText;
    el.classList.add("error");
  } else {
    el.textContent = "";
    el.classList.remove("error");
  }
}

function renderDeviceSelect(id, devices, kind) {
  const sel = $(id);
  if (!sel || !devices) return;

  // Don't rebuild while the user is interacting with the select.
  if (document.activeElement === sel) return;

  // Only rebuild when the device set or default has actually changed.
  const newKey = devices.map(d => d.name).join("|");
  const newDefault = currentDefault(devices);
  if (sel.dataset.key === newKey && sel.dataset.default === newDefault) return;
  sel.dataset.key = newKey;
  sel.dataset.default = newDefault;

  sel.innerHTML = devices.length === 0
    ? `<option value="">no devices found</option>`
    : devices.map(d =>
        `<option value="${escapeHTML(d.name)}" ${d.is_default ? "selected" : ""}>` +
          `${escapeHTML(d.description || d.name)}` +
        `</option>`
      ).join("");

  sel.onchange = async () => {
    const name = sel.value;
    console.log(`[devices] setting ${kind} to ${name}`);
    try {
      const r = await fetch("/api/v1/audio/devices", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ kind, name }),
      });
      if (!r.ok) {
        console.error(`[devices] set ${kind} failed:`, await r.text());
      }
      await loadDevices();
    } catch (err) {
      console.error(`[devices] network error:`, err);
    }
  };
}

function currentDefault(devices) {
  const d = devices.find(x => x.is_default);
  return d ? d.name : "";
}

// ── event wiring ────────────────────────────────────────

$("toggle-pc-audio")?.addEventListener("change", saveSettings);

$("volume-slider")?.addEventListener("input", e => {
  const pct = parseInt(e.target.value, 10);
  $("volume-pct").textContent = `${pct}%`;
  const db = pct <= 0 ? "-∞" : (20 * Math.log10(pct / 100)).toFixed(1);
  $("volume-db").textContent = `${db} dB`;
});

$("volume-slider")?.addEventListener("change", e => {
  saveVolume(parseInt(e.target.value, 10));
});