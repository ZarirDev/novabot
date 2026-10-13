// ── devices page (Wake-on-LAN) ─────────────────────────
console.log("[wol] devices.js loaded");

const wolState = {
  saved: [],   // persistent, on disk
  found: [],   // transient scan results
  editing: null, // MAC being edited, or null
};

// ── wiring ─────────────────────────────────────────────
function wolWireButtons() {
  const scanBtn = document.getElementById("wol-scan");
  if (scanBtn && !scanBtn.dataset.wired) {
    scanBtn.dataset.wired = "1";
    scanBtn.addEventListener("click", () => wolScan());
  }
  const addBtn = document.getElementById("wol-add-btn");
  if (addBtn && !addBtn.dataset.wired) {
    addBtn.dataset.wired = "1";
    addBtn.addEventListener("click", () => wolAddManual());
  }
  const cancelBtn = document.getElementById("wol-cancel-btn");
  if (cancelBtn && !cancelBtn.dataset.wired) {
    cancelBtn.dataset.wired = "1";
    cancelBtn.addEventListener("click", () => wolCancelEdit());
  }
}

// ── load / render ──────────────────────────────────────
async function wolLoad() {
  wolWireButtons();
  await wolLoadSaved();
}

async function wolLoadSaved() {
  try {
    const r = await fetch("/api/v1/wol/devices", { cache: "no-store" });
    if (!r.ok) {
      console.warn("[wol] GET /devices non-OK:", r.status);
      return;
    }
    const j = await r.json();
    wolState.saved = j.devices || [];
    wolRenderSaved();
  } catch (e) {
    console.error("[wol] load saved failed:", e);
  }
}

function wolRenderSaved() {
  const root = document.getElementById("wol-saved");
  if (!root) return;
  if (!wolState.saved.length) {
    root.innerHTML =
      '<div class="empty">no saved devices — scan or add one below</div>';
    return;
  }
  root.innerHTML = wolState.saved.map(d => {
    const hasBoot = d.boot_entries && d.boot_entries.length > 0;
    const opts = hasBoot
      ? d.boot_entries.map(e =>
          `<option value="${escapeHTML(e.id)}" data-source="${escapeHTML(e.source)}">` +
            `${escapeHTML(e.source === "efi" ? "efi · " : "grub · ")}${escapeHTML(e.title)}` +
          `</option>`
        ).join("")
      : "";
    const bootBlock = hasBoot ? `
      <div class="wol-boot">
        <label>boot next</label>
        <select class="wol-boot-select" data-mac="${escapeHTML(d.mac)}">
          ${opts}
        </select>
        <button class="wol-boot-go" data-mac="${escapeHTML(d.mac)}" type="button">set</button>
      </div>
    ` : "";
    return `
      <div class="wol-device">
        <div class="wol-device-info">
          <div class="name">${escapeHTML(d.name || d.ip || d.mac)}</div>
          <div class="meta">
            <span class="mac">${escapeHTML(d.mac)}</span>${d.ip ? ` · <span class="ip">${escapeHTML(d.ip)}</span>` : ""}
            ${hasBoot ? ` · <span class="boot-count">${d.boot_entries.length} boot entries</span>` : ""}
          </div>
        </div>
        <button class="wol-wake"   data-mac="${escapeHTML(d.mac)}" type="button">wake</button>
        <button class="wol-edit"   data-mac="${escapeHTML(d.mac)}" type="button">edit</button>
        <button class="wol-remove" data-mac="${escapeHTML(d.mac)}" type="button">remove</button>
        ${bootBlock}
      </div>
    `;
  }).join("");

  root.querySelectorAll(".wol-wake").forEach(b => {
    b.addEventListener("click", () => wolWake(b.dataset.mac, b));
  });
  root.querySelectorAll(".wol-edit").forEach(b => {
    b.addEventListener("click", () => wolStartEdit(b.dataset.mac));
  });
  root.querySelectorAll(".wol-remove").forEach(b => {
    b.addEventListener("click", () => wolRemove(b.dataset.mac));
  });
  root.querySelectorAll(".wol-boot-go").forEach(b => {
    b.addEventListener("click", () => {
      const sel = root.querySelector(`.wol-boot-select[data-mac="${CSS.escape(b.dataset.mac)}"]`);
      if (!sel) return;
      const opt = sel.options[sel.selectedIndex];
      wolBootNext(b.dataset.mac, sel.value, opt.dataset.source, opt.textContent.trim(), b);
    });
  });
}

async function wolBootNext(mac, id, source, title, btn) {
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const r = await fetch("/api/v1/wol/boot/" + encodeURIComponent(mac), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, source, title }),
    });
    if (!r.ok) throw new Error(await r.text());
    btn.textContent = "✓";
    setTimeout(() => { btn.textContent = old; btn.disabled = false; }, 1500);
  } catch (e) {
    alert("boot failed: " + e.message);
    btn.textContent = old;
    btn.disabled = false;
  }
}

function wolRenderFound() {
  const root = document.getElementById("wol-found");
  if (!root) return;
  if (!wolState.found.length) {
    root.innerHTML = '<div class="empty">no scan results yet</div>';
    return;
  }
  root.innerHTML = wolState.found.map((d, i) => `
    <div class="wol-device" data-idx="${i}">
      <div class="wol-device-info">
        <div class="name">${escapeHTML(d.name || d.ip || d.mac)}</div>
        <div class="meta">
          <span class="mac">${escapeHTML(d.mac)}</span>${d.ip ? ` · <span class="ip">${escapeHTML(d.ip)}</span>` : ""}
        </div>
      </div>
      <button class="wol-save" data-idx="${i}" type="button">save</button>
    </div>
  `).join("");

  root.querySelectorAll(".wol-save").forEach(b => {
    b.addEventListener("click", () => wolSaveFound(parseInt(b.dataset.idx, 10), b));
  });
}

// ── edit mode ──────────────────────────────────────────
function wolStartEdit(mac) {
  const d = wolState.saved.find(x => x.mac.toLowerCase() === mac.toLowerCase());
  if (!d) return;

  wolState.editing = d.mac;
  document.getElementById("wol-add-name").value = d.name || "";
  document.getElementById("wol-add-mac").value  = d.mac;
  document.getElementById("wol-add-ip").value   = d.ip || "";

  // Flip the form header + button label.
  const title = document.getElementById("wol-form-title");
  if (title) title.textContent = `editing ${d.name || d.mac}`;
  const saveBtn = document.getElementById("wol-add-btn");
  if (saveBtn) saveBtn.textContent = "update";
  const cancelBtn = document.getElementById("wol-cancel-btn");
  if (cancelBtn) cancelBtn.style.display = "";

  // Scroll the form into view.
  document.getElementById("wol-add-name")?.focus();
  document.querySelector("#page-devices .wol-add")?.scrollIntoView({
    behavior: "smooth", block: "center",
  });
}

function wolCancelEdit() {
  wolState.editing = null;
  document.getElementById("wol-add-name").value = "";
  document.getElementById("wol-add-mac").value  = "";
  document.getElementById("wol-add-ip").value   = "";
  const title = document.getElementById("wol-form-title");
  if (title) title.textContent = "add device manually";
  const saveBtn = document.getElementById("wol-add-btn");
  if (saveBtn) saveBtn.textContent = "save";
  const cancelBtn = document.getElementById("wol-cancel-btn");
  if (cancelBtn) cancelBtn.style.display = "none";
}

// ── actions ────────────────────────────────────────────
async function wolScan() {
  const btn = document.getElementById("wol-scan");
  if (!btn) return;

  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "scanning…";

  const root = document.getElementById("wol-found");
  if (root) root.innerHTML = '<div class="empty">scanning the lan…</div>';

  try {
    const r = await fetch("/api/v1/wol/scan", { method: "POST", cache: "no-store" });
    const text = await r.text();
    if (!r.ok) {
      alert(`scan failed (${r.status}): ${text.slice(0, 200)}`);
      return;
    }
    const j = JSON.parse(text);
    wolState.found = j.devices || [];
    wolRenderFound();
  } catch (e) {
    alert("scan error: " + e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = old;
  }
}

async function wolSaveFound(idx, btn) {
  const d = wolState.found[idx];
  if (!d) return;

  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "saving…";

  try {
    const r = await fetch("/api/v1/wol/devices", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: d.name || d.ip || d.mac, mac: d.mac, ip: d.ip }),
    });
    if (!r.ok) throw new Error(await r.text());
    wolState.found.splice(idx, 1);
    wolRenderFound();
    await wolLoadSaved();
  } catch (e) {
    alert("save failed: " + e.message);
    btn.disabled = false;
    btn.textContent = old;
  }
}

async function wolWake(mac, btn) {
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const r = await fetch("/api/v1/wol/wake/" + encodeURIComponent(mac), { method: "POST" });
    if (!r.ok) throw new Error(await r.text());
    btn.textContent = "✓";
    setTimeout(() => { btn.textContent = old; btn.disabled = false; }, 1200);
  } catch (e) {
    alert("wake failed: " + e.message);
    btn.textContent = old;
    btn.disabled = false;
  }
}

async function wolRemove(mac) {
  if (!confirm("remove " + mac + "?")) return;
  await fetch("/api/v1/wol/devices/" + encodeURIComponent(mac), { method: "DELETE" });
  if (wolState.editing && wolState.editing.toLowerCase() === mac.toLowerCase()) {
    wolCancelEdit();
  }
  await wolLoadSaved();
}

// wolAddManual handles both create and update. The backend upserts
// by MAC, so update is just a POST with the new values.
async function wolAddManual() {
  const nameEl = document.getElementById("wol-add-name");
  const macEl  = document.getElementById("wol-add-mac");
  const ipEl   = document.getElementById("wol-add-ip");

  const name = nameEl.value.trim();
  const mac  = macEl.value.trim();
  const ip   = ipEl.value.trim();

  if (!name || !mac) {
    alert("hostname and MAC are required");
    return;
  }

  // If we're editing an existing device and the user changed the MAC,
  // remove the old entry first so we don't end up with two.
  if (wolState.editing && wolState.editing.toLowerCase() !== mac.toLowerCase()) {
    await fetch("/api/v1/wol/devices/" + encodeURIComponent(wolState.editing), {
      method: "DELETE",
    });
  }

  const r = await fetch("/api/v1/wol/devices", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, mac, ip }),
  });
  if (!r.ok) {
    alert("save failed: " + (await r.text()));
    return;
  }

  wolCancelEdit();
  await wolLoadSaved();
}

// ── boot ───────────────────────────────────────────────
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    wolWireButtons();
    if (location.hash.startsWith("#/devices")) wolLoad();
  });
} else {
  wolWireButtons();
  if (location.hash.startsWith("#/devices")) wolLoad();
}