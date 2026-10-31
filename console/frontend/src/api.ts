// REST client for the console. The bearer token is kept in sessionStorage so a
// page reload within the same tab stays logged in, but closing the tab clears
// it.

import type { Device } from "./protocol";

const TOKEN_KEY = "nyk_token";
const USER_KEY = "nyk_user";

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function getUsername(): string | null {
  try {
    return sessionStorage.getItem(USER_KEY);
  } catch {
    return null;
  }
}

function setSession(token: string, username: string) {
  try {
    sessionStorage.setItem(TOKEN_KEY, token);
    sessionStorage.setItem(USER_KEY, username);
  } catch {
    /* private mode: stay in-memory only */
  }
}

function clearSession() {
  try {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(USER_KEY);
  } catch {
    /* ignore */
  }
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    throw new Error(res.status === 401 ? "Invalid username or password" : `Login failed (${res.status})`);
  }
  const data = (await res.json()) as { token: string; username: string };
  setSession(data.token, data.username);
}

export async function logout(): Promise<void> {
  const token = getToken();
  if (token) {
    try {
      await fetch("/api/logout", { method: "POST", headers: authHeader() });
    } catch {
      /* best effort */
    }
  }
  clearSession();
}

function authHeader(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

export async function fetchDevices(): Promise<Device[]> {
  const res = await fetch("/api/devices", { headers: authHeader() });
  if (res.status === 401) {
    clearSession();
    throw new Error("Session expired");
  }
  if (!res.ok) throw new Error(`Failed to load devices (${res.status})`);
  return ((await res.json()) as Device[]) ?? [];
}

export async function setNickname(id: string, nickname: string): Promise<void> {
  const res = await fetch("/api/nickname", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...authHeader() },
    body: JSON.stringify({ id, nickname }),
  });
  if (!res.ok) throw new Error(`Failed to save nickname (${res.status})`);
}

export interface AuditEntry {
  ts: number;
  event: string;
  tech: string;
  device: string;
  sessionId: string;
  detail: string;
}

export async function fetchAudit(): Promise<AuditEntry[]> {
  const res = await fetch("/api/audit", { headers: authHeader() });
  if (!res.ok) throw new Error(`Failed to load audit log (${res.status})`);
  return ((await res.json()) as AuditEntry[]) ?? [];
}

// wsURL builds the console WebSocket URL with the current token, using wss://
// when the page is served over https.
export function wsURL(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws/console?token=${encodeURIComponent(getToken() ?? "")}`;
}
