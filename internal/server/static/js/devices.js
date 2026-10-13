// ── devices page (Wake-on-LAN) ─────────────────────────
console.log("[wol] devices.js loading");

const wolState = { devices: [] };

// ── init: wire the buttons ─────────────────────────────
// All our scripts sit at the bottom of <body>, so the DOM is parsed
// by the time this runs — but guard anyway so a re-ordering of the
// script tags doesn't silently break us.
function wolInit() {
  console.log("[wol] init: document.readyState =", document.readyState);

  const scanBtn = document.getElementById("wol-scan");
  console.log("[wol] scan button:", scanBtn);
  if (scanBtn) {
    scanBtn.addEventListener("click", () => {
      console.log("[wol] >>> scan button clicked");
      wolScan();
    });
  } else {
    console.warn("[wol] scan button NOT FOUND — is #page-devices in the DOM?");
  }

  const addBtn = document.getElementById("wol-add-btn");
  console.log("[wol] add button:", addBtn);
  if (addBtn) {
    addBtn.addEventListener("click", () => {
      console.log("[wol] >>> add button clicked");
      wolAdd();
    });
  } else {
    console.warn("[wol] add button NOT FOUND");
  }

  // If we landed directly on #/devices, load immediately.
  if (location.hash.startsWith("#/devices")) {
    console.log("[wol] initial hash is #/devices — loading");
    wolLoad();
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", wolInit);
} else {
  wolInit();
}

// ── data ───────────────────────────────────────────────
async function wolLoad() {
  console.log("[wol] wolLoad()");
  try {
    const r = await fetch("/api/v1/wol/devices", { cache: "no-store" });
    console.log("[wol] GET /devices →", r.status);
    if (!r.ok) {
      console.warn("[wol] /devices non-OK:", r.status, await r.text());
      return;
    }
    const j = await r.json();
    wolState.devices = j.devices || [];
    console.log("[wol] loaded", wolState.devices.length, "devices");
    wolRender();
  } catch (e) {
    console.error("[wol] wolLoad failed:", e);
  }
}

function wolRender() {
  const root = document.getElementById("wol-devices");
  if (!root) {
    console.warn("[wol] render: #wol-devices not found");
    return;
  }
  if (!wolState.devices.length) {
    root.innerHTML =
      '<div class="empty">no devices — hit scan, or add one below</div>';
    return;
  }
  root.innerHTML = wolState.devices.map(d => `
    <div class="wol-device">
      <div class="wol-device-info">
        <div class="name">${escapeHTML(d.name || d.ip || d.mac)}</div>
        <div class="meta">${escapeHTML(d.mac)}${d.ip ? ' · ' + escapeHTML(d.ip) : ''}${d.manual ? ' · manual' : ''}</div>
      </div>
      <button class="wol-wake" data-mac="${escapeHTML(d.mac)}">wake</button>
      ${d.manual ? `<button class="wol-remove" data-mac="${escapeHTML(d.mac)}">remove</button>` : ''}
    </div>
  `).join("");

  root.querySelectorAll(".wol-wake").forEach(b => {
    b.addEventListener("click", () => wolWake(b.dataset.mac, b));
  });
  root.querySelectorAll(".wol-remove").forEach(b => {
    b.addEventListener("click", () => wolRemove(b.dataset.mac));
  });
}

async function wolScan() {
  console.log("[wol] wolScan() starting");
  const btn = document.getElementById("wol-scan");
  if (!btn) {
    console.warn("[wol] wolScan: button disappeared");
    return;
  }
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "scanning…";

  const t0 = performance.now();
  try {
    console.log("[wol] POST /api/v1/wol/scan");
    const r = await fetch("/api/v1/wol/scan", {
      method: "POST",
      cache: "no-store",
    });
    const elapsed = Math.round(performance.now() - t0);
    console.log(`[wol] scan response: ${r.status} in ${elapsed}ms`);

    const text = await r.text();
    console.log(`[wol] scan body (${text.length} bytes):`, text.slice(0, 400));

    if (!r.ok) {
      alert(`scan failed (${r.status}): ${text.slice(0, 200)}`);
      return;
    }
    let j;
    try {
      j = JSON.parse(text);
    } catch (parseErr) {
      console.error("[wol] scan body not JSON:", parseErr);
      alert("server returned invalid JSON");
      return;
    }
    console.log("[wol] scan found", j.found, "raw devices");
    wolState.devices = j.devices || [];
    console.log("[wol] after merge, list has", wolState.devices.length, "entries");
    wolRender();
  } catch (e) {
    console.error("[wol] scan network error:", e);
    alert("scan error: " + e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = old;
  }
}

async function wolWake(mac, btn) {
  console.log("[wol] wolWake", mac);
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const r = await fetch("/api/v1/wol/wake/" + encodeURIComponent(mac), {
      method: "POST",
    });
    console.log("[wol] wake response:", r.status);
    if (!r.ok) throw new Error(await r.text());
    btn.textContent = "✓";
    setTimeout(() => {
      btn.textContent = old;
      btn.disabled = false;
    }, 1200);
  } catch (e) {
    console.error("[wol] wake failed:", e);
    alert("wake failed: " + e.message);
    btn.textContent = old;
    btn.disabled = false;
  }
}

async function wolRemove(mac) {
  if (!confirm("remove " + mac + "?")) return;
  console.log("[wol] wolRemove", mac);
  const r = await fetch("/api/v1/wol/devices/" + encodeURIComponent(mac), {
    method: "DELETE",
  });
  console.log("[wol] remove response:", r.status);
  wolLoad();
}

async function wolAdd() {
  const name  = document.getElementById("wol-add-name").value.trim();
  const mac   = document.getElementById("wol-add-mac").value.trim();
  const ip    = document.getElementById("wol-add-ip").value.trim();
  const bcast = document.getElementById("wol-add-bcast").value.trim();
  console.log("[wol] wolAdd", { name, mac, ip, bcast });

  if (!name || !mac) {
    alert("name and MAC are required");
    return;
  }

  const r = await fetch("/api/v1/wol/devices", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, mac, ip, broadcast: bcast }),
  });
  console.log("[wol] add response:", r.status);
  if (!r.ok) {
    alert("add failed: " + (await r.text()));
    return;
  }

  document.getElementById("wol-add-name").value = "";
  document.getElementById("wol-add-mac").value = "";
  document.getElementById("wol-add-ip").value = "";
  document.getElementById("wol-add-bcast").value = "";
  wolLoad();
}