// Mirror of the Go `protocol` package. Keep these in sync with
// protocol/messages.go, protocol/input.go and protocol/control.go.

export const Type = {
  Hello: "hello",
  DeviceUpdate: "device_update",
  DeviceList: "device_list",
  Connect: "connect",
  ConnectRequest: "connect_request",
  Consent: "consent",
  SessionStart: "session_start",
  End: "end",
  Offer: "offer",
  Answer: "answer",
  ICE: "ice",
  Error: "error",
  Ping: "ping",
  Pong: "pong",
} as const;

export interface Monitor {
  index: number;
  label?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  primary?: boolean;
}

export interface Device {
  id: string;
  hostname: string;
  username: string;
  os: string;
  version: string;
  nickname?: string;
  online: boolean;
  lastSeen: number;
  monitors?: Monitor[];
}

export interface ICEServer {
  urls: string[];
  username?: string;
  credential?: string;
}

export interface Envelope {
  type: string;
  sessionId?: string;
  devices?: Device[];
  device?: Device;
  deviceId?: string;
  techName?: string;
  allowed?: boolean;
  iceServers?: ICEServer[];
  offerer?: boolean;
  sdp?: string;
  candidate?: string;
  error?: string;
  reason?: string;
}

// --- binary input codec (mirror of protocol/input.go) ---

export const Op = {
  MouseMove: 1,
  MouseBtn: 2,
  Wheel: 3,
  Key: 4,
} as const;

export const Btn = { Left: 0, Right: 1, Middle: 2 } as const;

export function encodeMouseMove(x: number, y: number): Uint8Array {
  const b = new Uint8Array(5);
  const dv = new DataView(b.buffer);
  b[0] = Op.MouseMove;
  dv.setUint16(1, x & 0xffff, true);
  dv.setUint16(3, y & 0xffff, true);
  return b;
}

export function encodeMouseButton(button: number, down: boolean): Uint8Array {
  return new Uint8Array([Op.MouseBtn, button, down ? 1 : 0]);
}

export function encodeWheel(dx: number, dy: number): Uint8Array {
  const b = new Uint8Array(5);
  const dv = new DataView(b.buffer);
  b[0] = Op.Wheel;
  dv.setInt16(1, clampInt16(dx), true);
  dv.setInt16(3, clampInt16(dy), true);
  return b;
}

export function encodeKey(scancode: number, extended: boolean, down: boolean): Uint8Array {
  const b = new Uint8Array(5);
  const dv = new DataView(b.buffer);
  b[0] = Op.Key;
  dv.setUint16(1, scancode & 0xffff, true);
  b[3] = extended ? 1 : 0;
  b[4] = down ? 1 : 0;
  return b;
}

function clampInt16(v: number): number {
  return Math.max(-32768, Math.min(32767, Math.round(v)));
}

// --- control channel (mirror of protocol/control.go) ---

export const Ctrl = {
  SelectMonitor: "selectMonitor",
  SetQuality: "setQuality",
  ClipboardText: "clipboardText",
  RequestKeyframe: "requestKeyframe",
  CtrlAltDel: "ctrlAltDel",
  Cursor: "cursor",
  Stats: "stats",
} as const;

export interface Control {
  kind: string;
  monitor?: number;
  quality?: string;
  text?: string;
  fps?: number;
  bitrate?: number;
}

export function formatDeviceID(id: string): string {
  if (id.length !== 9) return id;
  return `${id.slice(0, 3)} ${id.slice(3, 6)} ${id.slice(6, 9)}`;
}
