// ── devices page ──────────────────────────────────────────
console.log("[wol] devices.js v3 loaded");

const wolState = {
  saved: [],
  found: [],
  editing: null,
};

// wolLastKey lets us skip re-rendering when nothing changed. This is
// what stops the 3-second poll from clobbering the user's dropdown
// selection every cycle.
let wolLastKey = "";

// wolSelection remembers each device's dropdown choice so re-renders
// restore it instead of resetting to "default".
const wolSelection = {};

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

async function wolLoad() {
  wolWireButtons();
  await wolLoadSaved();
}

async function wolLoadSaved() {
  try {
    const r = await fetch("/api/v1/wol/devices", { cache: "no-store" });
    if (!r.ok) return;
    const j = await r.json();
    const newDevices = j.devices || [];
    const key = JSON.stringify(newDevices);
    if (key === wolLastKey) return;
    wolLastKey = key;
    wolState.saved = newDevices;
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

  // Preserve the current dropdown choices before blowing the DOM away.
  root.querySelectorAll(".wol-boot-select").forEach(sel => {
    if (sel.value) wolSelection[sel.dataset.mac] = sel.value;
  });

  root.innerHTML = wolState.saved.map(d => {
    const hasBoot = d.boot_entries && d.boot_entries.length > 0;
    const pending = d.pending_boot || null;

    const defaultOpt =
      `<option value="__default__" data-source="default">default (wake only)</option>`;
    const bootOpts = hasBoot
      ? defaultOpt + d.boot_entries.map(e =>
          `<option value="${escapeHTML(e.id)}" data-source="${escapeHTML(e.source)}">` +
            `${escapeHTML(e.source === "efi" ? "efi · " : "grub · ")}${escapeHTML(e.title)}` +
          `</option>`
        ).join("")
      : defaultOpt;

    const pendingBadge = pending
      ? `<div class="wol-pending">
           <span class="p-label">pending</span>
           <span class="p-what">${escapeHTML(pending.title || pending.id)}</span>
           <span class="p-src">${escapeHTML(pending.source)}</span>
           <button class="wol-cancel-pending" data-mac="${escapeHTML(d.mac)}" type="button">cancel</button>
         </div>`
      : "";

    return `
      <div class="wol-device">
        <div class="wol-device-info">
          <div class="name">${escapeHTML(d.name || d.ip || d.mac)}</div>
          <div class="meta">
            <span class="mac">${escapeHTML(d.mac)}</span>${d.ip ? ` · <span class="ip">${escapeHTML(d.ip)}</span>` : ""}
            ${hasBoot ? ` · <span class="boot-count">${d.boot_entries.length} boot entries</span>` : ""}
          </div>
        </div>
        <button class="wol-edit"   data-mac="${escapeHTML(d.mac)}" type="button">edit</button>
        <button class="wol-remove" data-mac="${escapeHTML(d.mac)}" type="button">remove</button>
        <div class="wol-boot">
          <label for="boot-${escapeHTML(d.mac)}">boot to</label>
          <select id="boot-${escapeHTML(d.mac)}" class="wol-boot-select" data-mac="${escapeHTML(d.mac)}">
            ${bootOpts}
          </select>
          <button class="wol-boot-go" data-mac="${escapeHTML(d.mac)}" type="button">go</button>
        </div>
        ${pendingBadge}
      </div>
    `;
  }).join("");

  // Restore any selections.
  root.querySelectorAll(".wol-boot-select").forEach(sel => {
    const prev = wolSelection[sel.dataset.mac];
    if (prev) {
      const opt = Array.from(sel.options).find(o => o.value === prev);
      if (opt) sel.value = prev;
    }
    sel.addEventListener("change", () => {
      wolSelection[sel.dataset.mac] = sel.value;
    });
  });

  root.querySelectorAll(".wol-edit").forEach(b => {
    b.addEventListener("click", () => wolStartEdit(b.dataset.mac));
  });
  root.querySelectorAll(".wol-remove").forEach(b => {
    b.addEventListener("click", () => wolRemove(b.dataset.mac));
  });
  root.querySelectorAll(".wol-boot-go").forEach(b => {
    b.addEventListener("click", () => {
      const mac = b.dataset.mac;
      const sel = root.querySelector(`.wol-boot-select[data-mac="${CSS.escape(mac)}"]`);
      if (!sel) return;
      const opt = sel.options[sel.selectedIndex];
      const source = opt.dataset.source || "efi";
      const id = sel.value === "__default__" ? "default" : sel.value;
      const title = opt.textContent.trim();
      console.log("[wol] GO clicked", { mac, id, source, title });
      wolBootNext(mac, id, source, title, b);
    });
  });
  root.querySelectorAll(".wol-cancel-pending").forEach(b => {
    b.addEventListener("click", () => wolCancelPending(b.dataset.mac, b));
  });
}

async function wolBootNext(mac, id, source, title, btn) {
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  console.log("[wol] POST /boot/" + mac, { id, source, title });
  try {
    const r = await fetch("/api/v1/wol/boot/" + encodeURIComponent(mac), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, source, title }),
    });
    const text = await r.text();
    if (!r.ok) throw new Error(text);
    let j = {};
    try { j = JSON.parse(text); } catch {}
    console.log("[wol] boot response:", j);
    const label = j.mode === "wol-only" ? "woke"
                : j.mode === "wol"      ? "queued"
                : j.mode === "direct"   ? "sent"
                : "✓";
    btn.textContent = label;
    setTimeout(() => { btn.textContent = old; btn.disabled = false; }, 2000);
    wolLastKey = ""; // force refresh so pending badge shows up
    setTimeout(wolLoadSaved, 300);
  } catch (e) {
    console.error("[wol] boot failed:", e);
    alert("boot failed: " + e.message);
    btn.textContent = old;
    btn.disabled = false;
  }
}

async function wolCancelPending(mac, btn) {
  if (!confirm("cancel pending boot for " + mac + "?")) return;
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const r = await fetch("/api/v1/wol/boot/" + encodeURIComponent(mac), {
      method: "DELETE",
    });
    if (!r.ok) throw new Error(await r.text());
    wolLastKey = "";
    await wolLoadSaved();
  } catch (e) {
    alert("cancel failed: " + e.message);
    btn.disabled = false;
    btn.textContent = old;
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

function wolStartEdit(mac) {
  const d = wolState.saved.find(x => x.mac.toLowerCase() === mac.toLowerCase());
  if (!d) return;
  wolState.editing = d.mac;
  document.getElementById("wol-add-name").value = d.name || "";
  document.getElementById("wol-add-mac").value  = d.mac;
  document.getElementById("wol-add-ip").value   = d.ip || "";
  const title = document.getElementById("wol-form-title");
  if (title) title.textContent = `editing ${d.name || d.mac}`;
  const saveBtn = document.getElementById("wol-add-btn");
  if (saveBtn) saveBtn.textContent = "update";
  const cancelBtn = document.getElementById("wol-cancel-btn");
  if (cancelBtn) cancelBtn.style.display = "";
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
    wolLastKey = "";
    await wolLoadSaved();
  } catch (e) {
    alert("save failed: " + e.message);
    btn.disabled = false;
    btn.textContent = old;
  }
}

async function wolRemove(mac) {
  if (!confirm("remove " + mac + "?")) return;
  await fetch("/api/v1/wol/devices/" + encodeURIComponent(mac), { method: "DELETE" });
  if (wolState.editing && wolState.editing.toLowerCase() === mac.toLowerCase()) {
    wolCancelEdit();
  }
  wolLastKey = "";
  await wolLoadSaved();
}

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
  wolLastKey = "";
  await wolLoadSaved();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    wolWireButtons();
    if (location.hash.startsWith("#/devices")) wolLoad();
  });
} else {
  wolWireButtons();
  if (location.hash.startsWith("#/devices")) wolLoad();
}