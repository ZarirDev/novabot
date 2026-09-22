// ── music page ──────────────────────────────────────────

let seeking = false;

async function musicStatus() {
  let s;
  try {
    const r = await fetch("/api/v1/music/status", { cache: "no-store" });
    s = await r.json();
  } catch {
    return;
  }

  $("np-title").textContent = s.title || "nothing playing";
  $("np-artist").textContent = s.artist || (s.idle ? "—" : "youtube");

  const pos = s.position || 0;
  const dur = s.duration || 0;

  $("np-pos").textContent = fmtTime(pos);
  $("np-dur").textContent = fmtTime(dur);

  // Freeze the seek bar while the user is dragging it, otherwise the
  // 1-second poll would fight their thumb.
  if (!seeking) {
    const seek = $("np-seek");
    seek.max = dur > 0 ? dur : 100;
    seek.value = pos;
  }

  // Toggle play/pause icons.
  $("icon-play").classList.toggle("hidden", !s.paused && !s.idle);
  $("icon-pause").classList.toggle("hidden", s.paused || s.idle);

  // mpv volume slider — don't fight the user if they're dragging.
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
            console.error(`[music] play failed (${res.status}):`, await res.text());
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

// ── event wiring ───────────────────────────────────────

// search
$("search-input")?.addEventListener("keydown", e => {
  if (e.key === "Enter") doSearch();
});
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

// seek — freeze the polling updates while dragging, commit on release
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

// mpv volume
const vol = $("np-vol");
vol?.addEventListener("input", () => {
  fetch("/api/v1/music/volume", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ volume: parseFloat(vol.value) }),
  });
});