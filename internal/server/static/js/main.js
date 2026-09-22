// ── bootstrap ───────────────────────────────────────────
// Loaded last. Wires up the router and starts the polling loops
// that keep each page fresh.

window.addEventListener("hashchange", route);
route();

// Dashboard — 1.5s, matches the OS stats update cadence.
setInterval(() => {
  if (currentPage === "dashboard") pollStats();
}, 1500);

// Music — 1s, so the progress bar feels responsive.
setInterval(() => {
  if (currentPage === "music") musicStatus();
}, 1000);

// Settings — stream status every 1s, everything else every 3s.
setInterval(() => {
  if (currentPage === "settings") pollAudioStatus();
}, 1000);

setInterval(() => {
  if (currentPage !== "settings") return;
  loadSettings();
  loadQuality();
  loadVolume();
  loadDevices();
}, 3000);