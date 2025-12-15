const $ = (id) => document.getElementById(id);

const state = {
  configId: null,
  connected: false,
  calibratedId: null,
  calSteps: [],
  calIndex: 0,
  ws: {
    test: null,
    cal: null,
    flash: null,
  },
};

function show(cardId) {
  for (const id of ["entryCard", "calibrationCard", "testCard", "flashCard"]) {
    $(id).classList.toggle("hidden", id !== cardId);
  }
}

function log(el, msg) {
  const line = `[${new Date().toLocaleTimeString()}] ${msg}\n`;
  el.textContent = line + el.textContent;
}

async function apiJSON(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `${res.status} ${res.statusText}`);
  return data;
}

async function uploadFile(url, file) {
  const fd = new FormData();
  fd.append("file", file);
  const res = await fetch(url, { method: "POST", body: fd });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `${res.status} ${res.statusText}`);
  return data;
}

function setStatus(text) {
  $("statusText").textContent = text;
}

function closeWS(ws) {
  try { ws?.close(); } catch {}
}

function connectWS(kind, url, onMsg) {
  closeWS(state.ws[kind]);
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const wsURL = `${proto}//${location.host}${url}`;
  const ws = new WebSocket(wsURL);
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    onMsg?.(msg);
  };
  ws.onclose = () => {};
  state.ws[kind] = ws;
}

async function uploadAndConnect() {
  const f = $("configFile").files?.[0];
  if (!f) throw new Error("Choose a config.json file first");
  const up = await uploadFile("/api/upload/config", f);
  state.configId = up.configId;
  log($("entryLog"), `Uploaded config -> id=${state.configId}`);
  const conn = await apiJSON("/api/connect", { configId: state.configId });
  state.connected = true;
  setStatus(`Connected on ${conn.port} (bars=${conn.bars}, lcs=${conn.lcs})`);
  log($("entryLog"), `Connected on ${conn.port}`);
}

async function disconnect() {
  await apiJSON("/api/disconnect");
  state.connected = false;
  state.configId = null;
  setStatus("Disconnected");
  log($("entryLog"), "Disconnected");
}

async function loadCalPlan() {
  const res = await fetch("/api/calibration/plan");
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || "failed to load plan");
  state.calSteps = data.steps || [];
  state.calIndex = 0;
  renderCalStep();
}

function renderCalStep() {
  const st = state.calSteps[state.calIndex];
  if (!st) {
    $("calStepText").textContent = "No plan loaded.";
    return;
  }
  $("calStepText").textContent = `Step ${st.stepIndex + 1}/${state.calSteps.length}  ${st.label}  —  ${st.prompt}`;
}

async function startCalStep() {
  connectWS("cal", "/ws/calibration", (msg) => {
    if (msg.type === "sample") {
      const d = msg.data || {};
      $("calLive").textContent = JSON.stringify(d, null, 2);
    }
    if (msg.type === "flashProgress") {
      log($("calLog"), `Flash: ${msg.data.stage} bar=${msg.data.barIndex + 1} ${msg.data.message}`);
    }
    if (msg.type === "stepDone") {
      log($("calLog"), `Step done: ${msg.data.label}`);
      $("calLive").textContent = "";
    }
    if (msg.type === "done") {
      state.calibratedId = msg.data.calibratedId;
      log($("calLog"), `Calibration complete. calibratedId=${state.calibratedId}`);
      log($("entryLog"), `Calibration complete. calibratedId=${state.calibratedId} (download via /api/download?id=${state.calibratedId})`);
      show("entryCard");
    }
    if (msg.type === "error") {
      log($("calLog"), `ERROR: ${msg.data.error}`);
    }
  });

  await apiJSON("/api/calibration/startStep", { stepIndex: state.calIndex });
  log($("calLog"), `Started step ${state.calIndex + 1}`);
}

async function startTest() {
  connectWS("test", "/ws/test", (msg) => {
    if (msg.type === "zerosProgress") {
      const z = msg.data || {};
      $("testZeros").textContent = `Collecting zeros: warmup ${z.warmupDone}/${z.warmupTarget}  samples ${z.sampleDone}/${z.sampleTarget}`;
    }
    if (msg.type === "zerosDone") {
      $("testZeros").textContent = "";
      log($("testLog"), "Zeros collected");
    }
    if (msg.type === "snapshot") {
      renderTestSnapshot(msg.data);
    }
    if (msg.type === "stopped") {
      log($("testLog"), "Stopped");
    }
    if (msg.type === "error") {
      log($("testLog"), `ERROR: ${msg.data.error}`);
    }
  });
  await apiJSON("/api/test/start");
  log($("testLog"), "Test started");
}

function renderTestSnapshot(snap) {
  if (!snap) return;
  $("testTotals").innerHTML = `
    <span class="pill">Grand total: <b>${Number(snap.grandTotal).toFixed(1)}</b></span>
  `;

  const perBar = snap.perBarLCWeight || [];
  const perBarTotal = snap.perBarTotal || [];
  const perBarADC = snap.perBarADC || [];

  let html = "";
  for (let bi = 0; bi < perBar.length; bi++) {
    html += `<div class="pill" style="margin-bottom:8px;">Bar ${bi + 1} total: <b>${Number(perBarTotal[bi] ?? 0).toFixed(1)}</b></div>`;
    html += `<table class="tbl"><thead><tr><th>LC</th><th>W</th><th>ADC</th></tr></thead><tbody>`;
    for (let li = 0; li < perBar[bi].length; li++) {
      html += `<tr><td>${li + 1}</td><td>${Number(perBar[bi][li]).toFixed(1)}</td><td>${perBarADC[bi]?.[li] ?? ""}</td></tr>`;
    }
    html += `</tbody></table><div style="height:12px;"></div>`;
  }
  $("testTable").innerHTML = html;
}

async function stopTest() {
  await apiJSON("/api/test/stop");
  closeWS(state.ws.test);
  log($("testLog"), "Stop requested");
}

async function uploadAndFlash() {
  const f = $("calibratedFile").files?.[0];
  if (!f) throw new Error("Choose a *_calibrated.json file first");
  const up = await uploadFile("/api/upload/calibrated", f);
  const calibratedId = up.configId;
  log($("flashLog"), `Uploaded calibrated -> id=${calibratedId}`);

  connectWS("flash", "/ws/flash", (msg) => {
    if (msg.type === "progress") {
      const p = msg.data || {};
      $("flashProgress").textContent = `Stage ${p.stage} bar ${p.barIndex + 1}: ${p.message}`;
    }
    if (msg.type === "done") {
      $("flashProgress").textContent = "Done";
      log($("flashLog"), "Flash complete");
      show("entryCard");
    }
    if (msg.type === "error") {
      log($("flashLog"), `ERROR: ${msg.data.error}`);
    }
  });

  await apiJSON("/api/flash/start", { calibratedId });
  log($("flashLog"), "Flash started");
}

async function stopFlash() {
  await apiJSON("/api/flash/stop");
  closeWS(state.ws.flash);
  $("flashProgress").textContent = "";
  log($("flashLog"), "Stop requested");
}

// Wire UI
$("btnUploadConnect").onclick = () => uploadAndConnect().catch((e) => log($("entryLog"), `ERROR: ${e.message}`));
$("btnDisconnect").onclick = () => disconnect().catch((e) => log($("entryLog"), `ERROR: ${e.message}`));

$("goCalibration").onclick = () => {
  if (!state.connected) return log($("entryLog"), "Connect first");
  show("calibrationCard");
  $("calLive").textContent = "";
  $("calLog").textContent = "";
  loadCalPlan().catch((e) => log($("calLog"), `ERROR: ${e.message}`));
};
$("goTest").onclick = () => {
  if (!state.connected) return log($("entryLog"), "Connect first");
  show("testCard");
  $("testLog").textContent = "";
  $("testTable").innerHTML = "";
  $("testTotals").innerHTML = "";
  $("testZeros").textContent = "";
};
$("goFlash").onclick = () => {
  if (!state.connected) return log($("entryLog"), "Connect first");
  show("flashCard");
  $("flashLog").textContent = "";
  $("flashProgress").textContent = "";
};

$("calBack").onclick = () => { apiJSON("/api/calibration/stop").finally(() => show("entryCard")); };
$("calPrev").onclick = () => { state.calIndex = Math.max(0, state.calIndex - 1); renderCalStep(); };
$("calNext").onclick = () => { state.calIndex = Math.min(state.calSteps.length - 1, state.calIndex + 1); renderCalStep(); };
$("calStartStep").onclick = () => startCalStep().catch((e) => log($("calLog"), `ERROR: ${e.message}`));

$("testBack").onclick = () => { stopTest().finally(() => show("entryCard")); };
$("testStart").onclick = () => startTest().catch((e) => log($("testLog"), `ERROR: ${e.message}`));
$("testStop").onclick = () => stopTest().catch((e) => log($("testLog"), `ERROR: ${e.message}`));

$("flashBack").onclick = () => { stopFlash().finally(() => show("entryCard")); };
$("flashUploadStart").onclick = () => uploadAndFlash().catch((e) => log($("flashLog"), `ERROR: ${e.message}`));
$("flashStop").onclick = () => stopFlash().catch((e) => log($("flashLog"), `ERROR: ${e.message}`));

// Initial
setStatus("Disconnected");
show("entryCard");

