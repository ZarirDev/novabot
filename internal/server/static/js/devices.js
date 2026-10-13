// ── devices page (Wake-on-LAN) ─────────────────────────
const wolState = { devices: [] };

async function wolLoad() {
  try {
    const r = await fetch('/api/v1/wol/devices');
    const j = await r.json();
    wolState.devices = j.devices || [];
    wolRender();
  } catch (e) {
    console.error('wol load', e);
  }
}

function wolRender() {
  const root = $('wol-devices');
  if (!root) return;
  if (!wolState.devices.length) {
    root.innerHTML = '<div class="empty">no devices — try scanning, or add one manually below</div>';
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
  `).join('');

  root.querySelectorAll('.wol-wake').forEach(b => {
    b.onclick = () => wolWake(b.dataset.mac, b);
  });
  root.querySelectorAll('.wol-remove').forEach(b => {
    b.onclick = () => wolRemove(b.dataset.mac);
  });
}

async function wolWake(mac, btn) {
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = '…';
  try {
    const r = await fetch('/api/v1/wol/wake/' + encodeURIComponent(mac), { method: 'POST' });
    if (!r.ok) throw new Error(await r.text());
    btn.textContent = '✓';
    setTimeout(() => { btn.textContent = old; btn.disabled = false; }, 1200);
  } catch (e) {
    alert('wake failed: ' + e.message);
    btn.textContent = old;
    btn.disabled = false;
  }
}

async function wolRemove(mac) {
  if (!confirm('remove ' + mac + '?')) return;
  await fetch('/api/v1/wol/devices/' + encodeURIComponent(mac), { method: 'DELETE' });
  wolLoad();
}

async function wolScan() {
  const btn = $('wol-scan');
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'scanning…';
  try {
    const r = await fetch('/api/v1/wol/scan', { method: 'POST' });
    const j = await r.json();
    wolState.devices = j.devices || [];
    wolRender();
  } catch (e) {
    alert('scan failed: ' + e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = old;
  }
}

async function wolAdd() {
  const name  = $('wol-add-name').value.trim();
  const mac   = $('wol-add-mac').value.trim();
  const ip    = $('wol-add-ip').value.trim();
  const bcast = $('wol-add-bcast').value.trim();
  if (!name || !mac) { alert('name and MAC are required'); return; }

  const r = await fetch('/api/v1/wol/devices', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, mac, ip, broadcast: bcast }),
  });
  if (!r.ok) { alert('add failed: ' + await r.text()); return; }

  $('wol-add-name').value = '';
  $('wol-add-mac').value = '';
  $('wol-add-ip').value = '';
  $('wol-add-bcast').value = '';
  wolLoad();
}

// Hook: the shared router only toggles page visibility. We load our
// data on hash change and on initial load if we started here.
window.addEventListener('load', () => {
  if (location.hash.startsWith('#/devices')) wolLoad();
});
window.addEventListener('hashchange', () => {
  if (location.hash.startsWith('#/devices')) wolLoad();
});