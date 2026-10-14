window.addEventListener("hashchange", route);
route();

setInterval(() => {
  if (currentPage === "dashboard") pollStats();
}, 1500);

setInterval(() => {
  if (currentPage === "music") musicStatus();
}, 1000);

setInterval(() => {
  if (currentPage === "settings") pollAudioStatus();
}, 1000);

setInterval(() => {
  if (currentPage !== "settings") return;
  loadSettings();
  loadQuality();
  loadVolume();
  loadDevices();
  loadLogging();
}, 3000);

// Poll the devices page so pending boot badges appear/clear promptly.
setInterval(() => {
  if (currentPage === "devices") wolLoad();
}, 3000);