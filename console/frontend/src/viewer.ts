// ViewerSession owns one live control session on the console side: the
// RTCPeerConnection that receives the client's screen as video, and the
// DataChannels that carry mouse/keyboard input and control messages.
//
// Signaling role: the console is always the WebRTC *offerer*. It creates the
// data channels and the recv-only video transceiver, makes the offer, and
// exchanges ICE with the agent through the signaling server.

import type { Envelope, Control } from "./protocol";
import { Type, Ctrl, encodeMouseMove, encodeMouseButton, encodeWheel, encodeKey, Btn } from "./protocol";
import { lookupScan } from "./keymap";
import { Signaling } from "./signaling";

export interface ViewerCallbacks {
  onTrack: (stream: MediaStream) => void;
  onEnd: (reason: string) => void;
  onStats?: (fps: number, bitrate: number) => void;
  onConnectionState?: (state: RTCPeerConnectionState) => void;
}

export class ViewerSession {
  readonly sessionId: string;
  private pc: RTCPeerConnection;
  private sig: Signaling;
  private input: RTCDataChannel;
  private control: RTCDataChannel;
  private cb: ViewerCallbacks;
  private controlling = true;
  private videoEl: HTMLVideoElement | null = null;

  // Throttle mouse-move: coalesce to one send per animation frame.
  private pendingMove: { x: number; y: number } | null = null;
  private moveScheduled = false;

  constructor(sessionId: string, iceServers: RTCIceServer[], sig: Signaling, cb: ViewerCallbacks) {
    this.sessionId = sessionId;
    this.sig = sig;
    this.cb = cb;

    this.pc = new RTCPeerConnection({ iceServers, bundlePolicy: "max-bundle" });
    this.pc.addTransceiver("video", { direction: "recvonly" });

    // Reliable, ordered channels. Input stays ordered so a key-up never
    // overtakes its key-down; moves are cheap enough that ordering is fine.
    this.input = this.pc.createDataChannel("input", { ordered: true });
    this.input.binaryType = "arraybuffer";
    this.control = this.pc.createDataChannel("control", { ordered: true });

    this.control.onmessage = (ev) => this.onControlMessage(ev.data);

    this.pc.ontrack = (ev) => {
      if (ev.streams[0]) this.cb.onTrack(ev.streams[0]);
    };
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

  // handleSignal feeds a signaling envelope addressed to this session.
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

  // --- input forwarding ---

  // bindInput attaches mouse/keyboard listeners to the video element and the
  // window. Call unbindInput() or stop() to remove them.
  bindInput(video: HTMLVideoElement) {
    this.videoEl = video;
    video.addEventListener("pointermove", this.onPointerMove);
    video.addEventListener("pointerdown", this.onPointerDown);
    window.addEventListener("pointerup", this.onPointerUp);
    video.addEventListener("wheel", this.onWheel, { passive: false });
    video.addEventListener("contextmenu", this.preventCtx);
    window.addEventListener("keydown", this.onKeyDown, true);
    window.addEventListener("keyup", this.onKeyUp, true);
  }

  unbindInput() {
    const video = this.videoEl;
    if (video) {
      video.removeEventListener("pointermove", this.onPointerMove);
      video.removeEventListener("pointerdown", this.onPointerDown);
      video.removeEventListener("wheel", this.onWheel);
      video.removeEventListener("contextmenu", this.preventCtx);
    }
    window.removeEventListener("pointerup", this.onPointerUp);
    window.removeEventListener("keydown", this.onKeyDown, true);
    window.removeEventListener("keyup", this.onKeyUp, true);
    this.videoEl = null;
  }

  setControlling(on: boolean) {
    this.controlling = on;
  }

  // Map a pointer event to normalized 0..65535 coordinates over the video's
  // rendered rectangle, accounting for object-fit: contain letterboxing.
  private normalize(ev: PointerEvent): { x: number; y: number } | null {
    const video = this.videoEl;
    if (!video || !video.videoWidth || !video.videoHeight) return null;
    const rect = video.getBoundingClientRect();
    const scale = Math.min(rect.width / video.videoWidth, rect.height / video.videoHeight);
    const renderW = video.videoWidth * scale;
    const renderH = video.videoHeight * scale;
    const padX = (rect.width - renderW) / 2;
    const padY = (rect.height - renderH) / 2;
    const px = ev.clientX - rect.left - padX;
    const py = ev.clientY - rect.top - padY;
    if (px < 0 || py < 0 || px > renderW || py > renderH) return null; // outside image
    const nx = Math.round((px / renderW) * 65535);
    const ny = Math.round((py / renderH) * 65535);
    return { x: Math.max(0, Math.min(65535, nx)), y: Math.max(0, Math.min(65535, ny)) };
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
    this.videoEl?.focus();
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
    // Normalize wheel to multiples of a notch (~120). Negative deltaY scrolls up.
    const dy = Math.trunc(-ev.deltaY);
    const dx = Math.trunc(-ev.deltaX);
    this.sendInput(encodeWheel(dx, dy));
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
      this.cb.onStats(c.fps ?? 0, c.bitrate ?? 0);
    } else if (c.kind === Ctrl.ClipboardText && c.text != null) {
      // Client copied something; offer it to the technician's clipboard.
      navigator.clipboard?.writeText(c.text).catch(() => {});
    }
  }

  // stop ends the session locally and tells the server.
  stop(notifyServer = true) {
    this.unbindInput();
    try {
      if (notifyServer) this.sig.send({ type: Type.End, sessionId: this.sessionId });
    } catch {
      /* ignore */
    }
    try {
      this.input.close();
    } catch {
      /* ignore */
    }
    try {
      this.control.close();
    } catch {
      /* ignore */
    }
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
