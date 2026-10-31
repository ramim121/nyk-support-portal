// ViewerSession owns one live control session on the console side. The client's
// screen arrives as compressed tiles on the "screen" DataChannel and is drawn
// onto a <canvas>; mouse/keyboard go back on "input"; small messages on
// "control". The console is the WebRTC offerer and creates all three channels.

import type { Control } from "./protocol";
import { Ctrl, Type, encodeMouseMove, encodeMouseButton, encodeWheel, encodeKey, Btn } from "./protocol";
import type { Envelope } from "./protocol";
import { lookupScan } from "./keymap";
import { parseScreen, mimeForCodec } from "./screen";
import { Signaling } from "./signaling";

export interface ViewerCallbacks {
  onEnd: (reason: string) => void;
  onStats?: (fps: number) => void;
  onConnectionState?: (state: RTCPeerConnectionState) => void;
  onResize?: (w: number, h: number) => void;
}

export class ViewerSession {
  readonly sessionId: string;
  private pc: RTCPeerConnection;
  private sig: Signaling;
  private input: RTCDataChannel;
  private control: RTCDataChannel;
  private screen: RTCDataChannel;
  private cb: ViewerCallbacks;
  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private controlling = true;
  private haveInfo = false;

  private pendingMove: { x: number; y: number } | null = null;
  private moveScheduled = false;

  constructor(
    sessionId: string,
    iceServers: RTCIceServer[],
    sig: Signaling,
    canvas: HTMLCanvasElement,
    cb: ViewerCallbacks
  ) {
    this.sessionId = sessionId;
    this.sig = sig;
    this.cb = cb;
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d", { alpha: false })!;

    this.pc = new RTCPeerConnection({ iceServers, bundlePolicy: "max-bundle" });

    this.input = this.pc.createDataChannel("input", { ordered: true });
    this.input.binaryType = "arraybuffer";
    this.control = this.pc.createDataChannel("control", { ordered: true });
    this.screen = this.pc.createDataChannel("screen", { ordered: true });
    this.screen.binaryType = "arraybuffer";

    this.control.onmessage = (ev) => this.onControlMessage(ev.data);
    this.screen.onmessage = (ev) => this.onScreenMessage(ev.data as ArrayBuffer);

    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) {
        this.sig.send({ type: Type.ICE, sessionId: this.sessionId, candidate: JSON.stringify(ev.candidate) });
      }
    };
    this.pc.onconnectionstatechange = () => {
      this.cb.onConnectionState?.(this.pc.connectionState);
      if (this.pc.connectionState === "failed" || this.pc.connectionState === "closed") {
        this.cb.onEnd("connection " + this.pc.connectionState);
      }
    };
  }

  async start() {
    const offer = await this.pc.createOffer();
    await this.pc.setLocalDescription(offer);
    this.sig.send({ type: Type.Offer, sessionId: this.sessionId, sdp: offer.sdp });
  }

  async handleSignal(env: Envelope) {
    try {
      if (env.type === Type.Answer && env.sdp) {
        await this.pc.setRemoteDescription({ type: "answer", sdp: env.sdp });
      } else if (env.type === Type.ICE && env.candidate) {
        await this.pc.addIceCandidate(JSON.parse(env.candidate));
      }
    } catch (e) {
      console.warn("signal error", e);
    }
  }

  // --- screen rendering ---

  private onScreenMessage(buf: ArrayBuffer) {
    const msg = parseScreen(buf);
    if (!msg) return;
    if (msg.kind === "info") {
      if (this.canvas.width !== msg.width || this.canvas.height !== msg.height) {
        this.canvas.width = msg.width;
        this.canvas.height = msg.height;
        this.ctx.fillStyle = "#000";
        this.ctx.fillRect(0, 0, msg.width, msg.height);
      }
      this.haveInfo = true;
      this.cb.onResize?.(msg.width, msg.height);
      return;
    }
    // tile — decode and draw. Copy the view so the underlying buffer can be
    // reused by the next message while decoding is in flight.
    const bytes = msg.data.slice();
    const blob = new Blob([bytes], { type: mimeForCodec(msg.codec) });
    createImageBitmap(blob)
      .then((bmp) => {
        this.ctx.drawImage(bmp, msg.x, msg.y);
        bmp.close();
      })
      .catch(() => {});
  }

  // --- input forwarding ---

  bindInput(canvas: HTMLCanvasElement) {
    canvas.addEventListener("pointermove", this.onPointerMove);
    canvas.addEventListener("pointerdown", this.onPointerDown);
    window.addEventListener("pointerup", this.onPointerUp);
    canvas.addEventListener("wheel", this.onWheel, { passive: false });
    canvas.addEventListener("contextmenu", this.preventCtx);
    window.addEventListener("keydown", this.onKeyDown, true);
    window.addEventListener("keyup", this.onKeyUp, true);
  }

  unbindInput() {
    this.canvas.removeEventListener("pointermove", this.onPointerMove);
    this.canvas.removeEventListener("pointerdown", this.onPointerDown);
    this.canvas.removeEventListener("wheel", this.onWheel);
    this.canvas.removeEventListener("contextmenu", this.preventCtx);
    window.removeEventListener("pointerup", this.onPointerUp);
    window.removeEventListener("keydown", this.onKeyDown, true);
    window.removeEventListener("keyup", this.onKeyUp, true);
  }

  setControlling(on: boolean) {
    this.controlling = on;
  }

  // Map a pointer event to normalized 0..65535 over the canvas image. The
  // canvas element is scaled to its own aspect ratio, so its client rect is the
  // image area (no letterboxing to account for).
  private normalize(ev: PointerEvent): { x: number; y: number } | null {
    if (!this.haveInfo || !this.canvas.width || !this.canvas.height) return null;
    const rect = this.canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return null;
    const px = ev.clientX - rect.left;
    const py = ev.clientY - rect.top;
    if (px < 0 || py < 0 || px > rect.width || py > rect.height) return null;
    const nx = Math.round((px / rect.width) * 65535);
    const ny = Math.round((py / rect.height) * 65535);
    return { x: clamp16(nx), y: clamp16(ny) };
  }

  private sendInput(buf: Uint8Array) {
    if (this.input.readyState === "open") this.input.send(buf);
  }

  private onPointerMove = (ev: PointerEvent) => {
    if (!this.controlling) return;
    const n = this.normalize(ev);
    if (!n) return;
    this.pendingMove = n;
    if (!this.moveScheduled) {
      this.moveScheduled = true;
      requestAnimationFrame(() => {
        this.moveScheduled = false;
        if (this.pendingMove) {
          this.sendInput(encodeMouseMove(this.pendingMove.x, this.pendingMove.y));
          this.pendingMove = null;
        }
      });
    }
  };

  private onPointerDown = (ev: PointerEvent) => {
    if (!this.controlling) return;
    ev.preventDefault();
    this.canvas.focus();
    const n = this.normalize(ev);
    if (n) this.sendInput(encodeMouseMove(n.x, n.y));
    this.sendInput(encodeMouseButton(mapButton(ev.button), true));
  };

  private onPointerUp = (ev: PointerEvent) => {
    if (!this.controlling) return;
    this.sendInput(encodeMouseButton(mapButton(ev.button), false));
  };

  private onWheel = (ev: WheelEvent) => {
    if (!this.controlling) return;
    ev.preventDefault();
    this.sendInput(encodeWheel(Math.trunc(-ev.deltaX), Math.trunc(-ev.deltaY)));
  };

  private preventCtx = (ev: Event) => ev.preventDefault();

  private onKeyDown = (ev: KeyboardEvent) => {
    if (!this.controlling) return;
    const scan = lookupScan(ev.code);
    if (!scan) return;
    ev.preventDefault();
    this.sendInput(encodeKey(scan.code, scan.ext, true));
  };

  private onKeyUp = (ev: KeyboardEvent) => {
    if (!this.controlling) return;
    const scan = lookupScan(ev.code);
    if (!scan) return;
    ev.preventDefault();
    this.sendInput(encodeKey(scan.code, scan.ext, false));
  };

  // --- control channel ---

  private sendControl(c: Control) {
    if (this.control.readyState === "open") this.control.send(JSON.stringify(c));
  }

  selectMonitor(index: number) {
    this.sendControl({ kind: Ctrl.SelectMonitor, monitor: index });
  }
  setQuality(q: string) {
    this.sendControl({ kind: Ctrl.SetQuality, quality: q });
  }
  requestKeyframe() {
    this.sendControl({ kind: Ctrl.RequestKeyframe });
  }
  sendClipboard(text: string) {
    this.sendControl({ kind: Ctrl.ClipboardText, text });
  }

  private onControlMessage(data: string) {
    let c: Control;
    try {
      c = JSON.parse(data) as Control;
    } catch {
      return;
    }
    if (c.kind === Ctrl.Stats && this.cb.onStats) {
      this.cb.onStats(c.fps ?? 0);
    } else if (c.kind === Ctrl.ClipboardText && c.text != null) {
      navigator.clipboard?.writeText(c.text).catch(() => {});
    }
  }

  stop(notifyServer = true) {
    this.unbindInput();
    try {
      if (notifyServer) this.sig.send({ type: Type.End, sessionId: this.sessionId });
    } catch {
      /* ignore */
    }
    [this.input, this.control, this.screen].forEach((dc) => {
      try {
        dc.close();
      } catch {
        /* ignore */
      }
    });
    this.pc.close();
  }
}

function mapButton(domButton: number): number {
  switch (domButton) {
    case 2:
      return Btn.Right;
    case 1:
      return Btn.Middle;
    default:
      return Btn.Left;
  }
}

function clamp16(v: number): number {
  return Math.max(0, Math.min(65535, v));
}
