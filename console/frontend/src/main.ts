import "./style.css";
import { Signaling } from "./signaling";
import { ViewerSession } from "./viewer";
import { Type, formatDeviceID } from "./protocol";
import type { Device, Envelope } from "./protocol";
import * as api from "./api";

// --- element helpers ---
const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

const loginView = $("login");
const appView = $("app");
const loginForm = $<HTMLFormElement>("login-form");
const loginError = $("login-error");

// --- app state ---
let sig: Signaling | null = null;
const devices = new Map<string, Device>();
let pendingDeviceId: string | null = null; // awaiting consent
let viewer: ViewerSession | null = null;
let fitMode = true;

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

loginForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  loginError.hidden = true;
  const btn = $<HTMLButtonElement>("login-btn");
  btn.disabled = true;
  try {
    await api.login($<HTMLInputElement>("username").value.trim(), $<HTMLInputElement>("password").value);
    enterApp();
  } catch (err) {
    loginError.textContent = (err as Error).message;
    loginError.hidden = false;
  } finally {
    btn.disabled = false;
  }
});

$("logout-btn").addEventListener("click", async () => {
  await api.logout();
  location.reload();
});

function enterApp() {
  loginView.hidden = true;
  appView.hidden = false;
  $("who").textContent = api.getUsername() ?? "";
  sig = new Signaling(onSignal, onSignalStatus);
  sig.connect();
}

function onSignalStatus(connected: boolean) {
  const dot = $("conn-dot");
  dot.classList.toggle("online", connected);
  dot.classList.toggle("offline", !connected);
}

// ---------------------------------------------------------------------------
// Signaling messages
// ---------------------------------------------------------------------------

function onSignal(env: Envelope) {
  switch (env.type) {
    case Type.DeviceList:
      devices.clear();
      (env.devices ?? []).forEach((d) => devices.set(d.id, d));
      renderDevices();
      break;
    case Type.DeviceUpdate:
      if (env.device) {
        devices.set(env.device.id, env.device);
        renderDevices();
      }
      break;
    case Type.SessionStart:
      if (env.offerer && env.sessionId) startViewer(env);
      break;
    case Type.Offer:
    case Type.Answer:
    case Type.ICE:
      if (viewer && env.sessionId === viewer.sessionId) viewer.handleSignal(env);
      break;
    case Type.Error:
      toast(env.error ?? "Error");
      if (pendingDeviceId) setConnecting(pendingDeviceId, false), (pendingDeviceId = null);
      break;
    case Type.End:
      if (viewer && env.sessionId === viewer.sessionId) endViewer(env.reason ?? "Session ended");
      break;
  }
}

// ---------------------------------------------------------------------------
// Device list
// ---------------------------------------------------------------------------

$<HTMLInputElement>("search").addEventListener("input", renderDevices);

function renderDevices() {
  const q = $<HTMLInputElement>("search").value.trim().toLowerCase();
  const list = $("device-list");
  const all = [...devices.values()].sort((a, b) => {
    if (a.online !== b.online) return a.online ? -1 : 1;
    return (a.nickname || a.hostname).localeCompare(b.nickname || b.hostname);
  });
  const filtered = all.filter((d) => {
    if (!q) return true;
    return (
      d.hostname.toLowerCase().includes(q) ||
      (d.nickname ?? "").toLowerCase().includes(q) ||
      d.username.toLowerCase().includes(q) ||
      d.id.includes(q.replace(/\s/g, ""))
    );
  });
  $("empty-hint").hidden = all.length > 0;

  list.innerHTML = "";
  for (const d of filtered) {
    const li = document.createElement("li");
    li.className = "device" + (d.online ? " online" : "");
    if (pendingDeviceId === d.id) li.classList.add("connecting");

    const name = d.nickname || d.hostname || "(unknown)";
    li.innerHTML = `
      <span class="dot ${d.online ? "online" : "offline"}"></span>
      <div class="dmeta">
        <div class="dname" title="Double-click to rename">${escapeHtml(name)}</div>
        <div class="dsub">${escapeHtml(d.username)} · ${formatDeviceID(d.id)}</div>
      </div>
      <button class="connect" ${d.online ? "" : "disabled"}>${pendingDeviceId === d.id ? "Waiting…" : "Connect"}</button>
    `;
    li.querySelector(".connect")!.addEventListener("click", () => connectTo(d.id));
    li.querySelector(".dname")!.addEventListener("dblclick", () => renameDevice(d));
    list.appendChild(li);
  }
}

async function renameDevice(d: Device) {
  const nickname = prompt(`Nickname for ${d.hostname}`, d.nickname || "");
  if (nickname == null) return;
  try {
    await api.setNickname(d.id, nickname);
    d.nickname = nickname;
    renderDevices();
  } catch (e) {
    toast((e as Error).message);
  }
}

function connectTo(deviceId: string) {
  if (viewer) {
    toast("End the current session first.");
    return;
  }
  pendingDeviceId = deviceId;
  setConnecting(deviceId, true);
  sig?.send({ type: Type.Connect, deviceId });
  toast("Waiting for the client to allow the session…");
}

function setConnecting(_deviceId: string, _on: boolean) {
  renderDevices();
}

// ---------------------------------------------------------------------------
// Viewer
// ---------------------------------------------------------------------------

function startViewer(env: Envelope) {
  pendingDeviceId = null;
  const dev = env.deviceId ? devices.get(env.deviceId) : undefined;
  const ice = (env.iceServers ?? []).map((s) => ({
    urls: s.urls,
    username: s.username,
    credential: s.credential,
  })) as RTCIceServer[];

  const canvas = $<HTMLCanvasElement>("screen");
  viewer = new ViewerSession(env.sessionId!, ice, sig!, canvas, {
    onEnd: (reason) => endViewer(reason),
    onStats: (fps) => {
      $("viewer-state").textContent = `${fps} fps`;
    },
    onConnectionState: (st) => {
      if (st === "connected") $("viewer-state").textContent = "connected";
      else if (st === "connecting") $("viewer-state").textContent = "connecting…";
    },
  });

  // Populate the monitor picker from the device's reported displays.
  const sel = $<HTMLSelectElement>("monitor-select");
  sel.innerHTML = "";
  const mons = dev?.monitors ?? [];
  if (mons.length <= 1) {
    sel.innerHTML = `<option value="0">Primary</option>`;
    sel.disabled = true;
  } else {
    sel.disabled = false;
    mons.forEach((m) =>
      sel.add(new Option(`${m.primary ? "★ " : ""}${m.label || "Display " + (m.index + 1)} (${m.width}×${m.height})`, String(m.index)))
    );
  }

  $("viewer-title").textContent = dev ? `${dev.nickname || dev.hostname} — ${dev.username}` : "Remote session";
  $("viewer-state").textContent = "connecting…";
  $("stage-idle").hidden = true;
  $("viewer").hidden = false;

  viewer.bindInput(canvas);
  viewer.start().catch((e) => {
    toast("Failed to start: " + (e as Error).message);
    endViewer("start failed");
  });
  renderDevices();
}

function endViewer(reason: string) {
  if (viewer) {
    viewer.stop(false);
    viewer = null;
  }
  const canvas = $<HTMLCanvasElement>("screen");
  const cctx = canvas.getContext("2d");
  cctx?.clearRect(0, 0, canvas.width, canvas.height);
  $("viewer").hidden = true;
  $("stage-idle").hidden = false;
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  toast(reason);
  renderDevices();
}

$("disconnect-btn").addEventListener("click", () => {
  if (viewer) {
    viewer.stop(true);
    viewer = null;
    endViewer("Disconnected");
  }
});

$<HTMLSelectElement>("monitor-select").addEventListener("change", (e) => {
  viewer?.selectMonitor(parseInt((e.target as HTMLSelectElement).value, 10));
});

$<HTMLSelectElement>("quality-select").addEventListener("change", (e) => {
  viewer?.setQuality((e.target as HTMLSelectElement).value);
});

$("fit-btn").addEventListener("click", () => {
  fitMode = !fitMode;
  $("video-wrap").classList.toggle("actual", !fitMode);
  $("fit-btn").textContent = fitMode ? "Fit" : "Actual";
});

$("clip-btn").addEventListener("click", async () => {
  try {
    const text = await navigator.clipboard.readText();
    viewer?.sendClipboard(text);
    toast("Clipboard sent to client");
  } catch {
    toast("Clipboard permission denied by the browser");
  }
});

$("fs-btn").addEventListener("click", () => {
  const wrap = $("video-wrap");
  if (!document.fullscreenElement) wrap.requestFullscreen().catch(() => {});
  else document.exitFullscreen().catch(() => {});
});

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

let toastTimer: number | undefined;
function toast(msg: string) {
  const t = $("toast");
  t.textContent = msg;
  t.hidden = false;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => (t.hidden = true), 4000);
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]!));
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------

if (api.getToken()) {
  enterApp();
}
