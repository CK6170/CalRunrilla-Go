import { useEffect, useMemo, useState } from 'react';
import './App.css';
import {
  Connect,
  Disconnect,
  GetCalibrationPlan,
  StartCalibrationStep,
  StartFlash,
  StartTest,
  StopTest,
  CancelOperation,
} from "../wailsjs/go/main/App";

import {
  EventsOn,
  EventsOff,
  OpenFileDialog,
} from "../wailsjs/runtime/runtime";

function App() {
  const [page, setPage] = useState("entry"); // entry | calibration | test | flash

  const [configPath, setConfigPath] = useState("");
  const [connected, setConnected] = useState(false);
  const [connInfo, setConnInfo] = useState(null);
  const [error, setError] = useState("");

  // calibration
  const [calPlan, setCalPlan] = useState([]);
  const [calStepIndex, setCalStepIndex] = useState(0);
  const [calSample, setCalSample] = useState(null);
  const [calFlash, setCalFlash] = useState(null);
  const [calDoneInfo, setCalDoneInfo] = useState(null);

  // test
  const [testZeroProg, setTestZeroProg] = useState(null);
  const [testSnap, setTestSnap] = useState(null);
  const [testRunning, setTestRunning] = useState(false);

  // flash
  const [calibratedPath, setCalibratedPath] = useState("");
  const [flashProg, setFlashProg] = useState(null);
  const [flashRunning, setFlashRunning] = useState(false);

  const title = useMemo(() => {
    if (!connected) return "Disconnected";
    return `Connected on ${connInfo?.port ?? "?"} (bars=${connInfo?.bars ?? "?"}, lcs=${connInfo?.lcs ?? "?"})`;
  }, [connected, connInfo]);

  useEffect(() => {
    const subs = [];

    const on = (name, fn) => {
      EventsOn(name, fn);
      subs.push({ name, fn });
    };

    on("device:connected", (info) => {
      setConnected(true);
      setConnInfo(info);
      setError("");
      setCalibratedPath("");
    });
    on("device:disconnected", () => {
      setConnected(false);
      setConnInfo(null);
      setPage("entry");
      setTestRunning(false);
      setFlashRunning(false);
    });

    on("calibration:sample", (p) => setCalSample(p));
    on("calibration:flashProgress", (p) => setCalFlash(p));
    on("calibration:stepDone", () => {
      setCalSample(null);
      setError("");
    });
    on("calibration:done", (info) => {
      setCalDoneInfo(info);
      setPage("entry");
    });
    on("calibration:error", (msg) => setError(String(msg)));

    on("test:zerosProgress", (p) => setTestZeroProg(p));
    on("test:zerosDone", () => setTestZeroProg(null));
    on("test:snapshot", (snap) => setTestSnap(snap));
    on("test:stopped", () => setTestRunning(false));
    on("test:error", (msg) => {
      setError(String(msg));
      setTestRunning(false);
    });

    on("flash:progress", (p) => setFlashProg(p));
    on("flash:done", () => {
      setFlashRunning(false);
      setPage("entry");
    });
    on("flash:error", (msg) => {
      setError(String(msg));
      setFlashRunning(false);
    });

    return () => {
      for (const s of subs) {
        EventsOff(s.name);
      }
    };
  }, []);

  async function pickConfig() {
    const path = await OpenFileDialog({
      Title: "Select config.json",
      Filters: [{ DisplayName: "JSON", Pattern: "*.json" }],
    });
    if (path) setConfigPath(path);
  }

  async function pickCalibrated() {
    const path = await OpenFileDialog({
      Title: "Select _calibrated.json",
      Filters: [{ DisplayName: "JSON", Pattern: "*.json" }],
    });
    if (path) setCalibratedPath(path);
  }

  async function doConnect() {
    setError("");
    try {
      const info = await Connect(configPath);
      setConnInfo(info);
      setConnected(true);
    } catch (e) {
      setError(String(e));
    }
  }

  async function doDisconnect() {
    setError("");
    try {
      await Disconnect();
    } catch (e) {
      setError(String(e));
    }
  }

  async function goCalibration() {
    setError("");
    try {
      const plan = await GetCalibrationPlan();
      setCalPlan(plan);
      setCalStepIndex(0);
      setCalSample(null);
      setCalFlash(null);
      setPage("calibration");
    } catch (e) {
      setError(String(e));
    }
  }

  async function startCalStep() {
    setError("");
    setCalDoneInfo(null);
    setCalFlash(null);
    try {
      await StartCalibrationStep(calStepIndex);
    } catch (e) {
      setError(String(e));
    }
  }

  async function nextCalStep() {
    setCalStepIndex((i) => Math.min(i + 1, calPlan.length - 1));
    setCalSample(null);
  }

  async function prevCalStep() {
    setCalStepIndex((i) => Math.max(i - 1, 0));
    setCalSample(null);
  }

  async function goTest() {
    setError("");
    setTestSnap(null);
    setTestZeroProg(null);
    setPage("test");
  }

  async function startTest() {
    setError("");
    setTestSnap(null);
    setTestZeroProg(null);
    setTestRunning(true);
    try {
      await StartTest();
    } catch (e) {
      setError(String(e));
      setTestRunning(false);
    }
  }

  async function stopTest() {
    setError("");
    try {
      await StopTest();
      setTestRunning(false);
    } catch (e) {
      setError(String(e));
    }
  }

  async function goFlash() {
    setError("");
    setFlashProg(null);
    setPage("flash");
  }

  async function startFlash() {
    setError("");
    setFlashRunning(true);
    setFlashProg(null);
    try {
      await StartFlash(calibratedPath);
    } catch (e) {
      setError(String(e));
      setFlashRunning(false);
    }
  }

  async function backToEntry() {
    setError("");
    try {
      await CancelOperation();
    } catch (_) {}
    setPage("entry");
  }

  return (
    <div id="App" style={{ padding: 20, textAlign: "left" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <h2>Calrunrilla Desktop UI</h2>
        <div style={{ opacity: 0.8 }}>{title}</div>
      </div>

      {error ? (
        <div style={{ background: "#3b1d1d", border: "1px solid #a33", padding: 12, marginBottom: 12 }}>
          <b>Error:</b> {error}
        </div>
      ) : null}

      {page === "entry" ? (
        <div>
          <h3>Entry</h3>
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 10 }}>
            <input
              style={{ flex: 1, padding: 10 }}
              value={configPath}
              placeholder="Path to config.json"
              onChange={(e) => setConfigPath(e.target.value)}
            />
            <button className="btn" onClick={pickConfig}>Browse…</button>
            {!connected ? (
              <button className="btn" onClick={doConnect} disabled={!configPath}>Connect</button>
            ) : (
              <button className="btn" onClick={doDisconnect}>Disconnect</button>
            )}
          </div>

          {calDoneInfo ? (
            <div style={{ marginBottom: 12 }}>
              Calibration saved: <b>{calDoneInfo.calibratedFile}</b>
            </div>
          ) : null}

          <div style={{ display: "flex", gap: 8 }}>
            <button className="btn" onClick={goCalibration} disabled={!connected}>Calibration</button>
            <button className="btn" onClick={goTest} disabled={!connected}>Test</button>
            <button className="btn" onClick={goFlash} disabled={!connected}>Flash</button>
          </div>
        </div>
      ) : null}

      {page === "calibration" ? (
        <div>
          <h3>Calibration</h3>
          <div style={{ marginBottom: 10 }}>
            Step {calStepIndex + 1} / {calPlan.length}
          </div>
          <div style={{ marginBottom: 10 }}>
            <b>{calPlan[calStepIndex]?.label}</b> — {calPlan[calStepIndex]?.prompt}
          </div>
          <div style={{ display: "flex", gap: 8, marginBottom: 10 }}>
            <button className="btn" onClick={prevCalStep} disabled={calStepIndex === 0}>Prev</button>
            <button className="btn" onClick={nextCalStep} disabled={calStepIndex >= calPlan.length - 1}>Next</button>
            <button className="btn" onClick={startCalStep}>Sample this step</button>
            <button className="btn" onClick={backToEntry}>Back</button>
          </div>

          {calSample ? (
            <div style={{ marginTop: 10 }}>
              <div>
                Sampling: <b>{calSample.phase}</b> — ignore {calSample.ignoreDone}/{calSample.ignoreTarget} avg {calSample.avgDone}/{calSample.avgTarget}
              </div>
              {calSample.current ? (
                <pre style={{ marginTop: 10, padding: 10, background: "#0f172a" }}>
                  {JSON.stringify(calSample.current, null, 2)}
                </pre>
              ) : null}
              {calSample.final ? (
                <pre style={{ marginTop: 10, padding: 10, background: "#0f172a" }}>
                  Final averages: {JSON.stringify(calSample.final, null, 2)}
                </pre>
              ) : null}
            </div>
          ) : null}

          {calFlash ? (
            <div style={{ marginTop: 10 }}>
              Flashing: <b>{calFlash.stage}</b> bar {calFlash.barIndex + 1} — {calFlash.message}
            </div>
          ) : null}
        </div>
      ) : null}

      {page === "test" ? (
        <div>
          <h3>Test (live weights)</h3>
          <div style={{ display: "flex", gap: 8, marginBottom: 10 }}>
            {!testRunning ? (
              <button className="btn" onClick={startTest}>Start</button>
            ) : (
              <button className="btn" onClick={stopTest}>Stop</button>
            )}
            <button className="btn" onClick={backToEntry}>Back</button>
          </div>
          {testZeroProg ? (
            <div style={{ marginBottom: 10 }}>
              Collecting zeros: warmup {testZeroProg.warmupDone}/{testZeroProg.warmupTarget} samples {testZeroProg.sampleDone}/{testZeroProg.sampleTarget}
            </div>
          ) : null}

          {testSnap ? (
            <div>
              <h4>Grand total: {testSnap.grandTotal.toFixed(1)}</h4>
              {testSnap.perBarLCWeight.map((bar, bi) => (
                <div key={bi} style={{ marginBottom: 14, padding: 10, border: "1px solid #1f2937" }}>
                  <div><b>Bar {bi + 1}</b> — total: {testSnap.perBarTotal[bi].toFixed(1)}</div>
                  <table style={{ width: "100%", marginTop: 8 }}>
                    <thead>
                      <tr>
                        <th style={{ textAlign: "left" }}>LC</th>
                        <th style={{ textAlign: "left" }}>Weight</th>
                        <th style={{ textAlign: "left" }}>ADC</th>
                      </tr>
                    </thead>
                    <tbody>
                      {bar.map((w, li) => (
                        <tr key={li}>
                          <td>{li + 1}</td>
                          <td>{w.toFixed(1)}</td>
                          <td>{testSnap.perBarADC[bi][li]}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {page === "flash" ? (
        <div>
          <h3>Flash</h3>
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 10 }}>
            <input
              style={{ flex: 1, padding: 10 }}
              value={calibratedPath}
              placeholder="Path to *_calibrated.json"
              onChange={(e) => setCalibratedPath(e.target.value)}
            />
            <button className="btn" onClick={pickCalibrated}>Browse…</button>
            <button className="btn" onClick={startFlash} disabled={!calibratedPath || flashRunning}>
              {flashRunning ? "Flashing..." : "Flash"}
            </button>
            <button className="btn" onClick={backToEntry}>Back</button>
          </div>
          {flashProg ? (
            <div>
              Flashing: <b>{flashProg.stage}</b> bar {flashProg.barIndex + 1} — {flashProg.message}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export default App
