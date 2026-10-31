// Thin WebSocket client for the console's signaling channel. It reconnects with
// backoff and surfaces typed envelopes to a handler.

import type { Envelope } from "./protocol";
import { Type } from "./protocol";
import { wsURL } from "./api";

type Handler = (env: Envelope) => void;
type StatusHandler = (connected: boolean) => void;

export class Signaling {
  private ws: WebSocket | null = null;
  private onMsg: Handler;
  private onStatus: StatusHandler;
  private closedByUs = false;
  private backoff = 1000;
  private pingTimer: number | undefined;

  constructor(onMsg: Handler, onStatus: StatusHandler) {
    this.onMsg = onMsg;
    this.onStatus = onStatus;
  }

  connect() {
    this.closedByUs = false;
    this.open();
  }

  private open() {
    const ws = new WebSocket(wsURL());
    this.ws = ws;
    ws.onopen = () => {
      this.backoff = 1000;
      this.onStatus(true);
      this.pingTimer = window.setInterval(() => this.send({ type: Type.Ping }), 25000);
    };
    ws.onmessage = (ev) => {
      let env: Envelope;
      try {
        env = JSON.parse(ev.data) as Envelope;
      } catch {
        return;
      }
      this.onMsg(env);
    };
    ws.onclose = () => {
      window.clearInterval(this.pingTimer);
      this.onStatus(false);
      if (!this.closedByUs) {
        setTimeout(() => this.open(), this.backoff);
        this.backoff = Math.min(this.backoff * 2, 15000);
      }
    };
    ws.onerror = () => ws.close();
  }

  send(env: Envelope) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(env));
    }
  }

  close() {
    this.closedByUs = true;
    window.clearInterval(this.pingTimer);
    this.ws?.close();
  }
}
