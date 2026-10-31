// Parser for the "screen" DataChannel messages (mirror of protocol/screen.go).

export const ScreenMsg = { Tile: 1, Info: 2 } as const;

export interface ScreenInfo {
  kind: "info";
  monitorIndex: number;
  width: number;
  height: number;
}

export interface ScreenTile {
  kind: "tile";
  x: number;
  y: number;
  w: number;
  h: number;
  codec: number;
  data: Uint8Array;
}

export type ScreenMessage = ScreenInfo | ScreenTile;

export function parseScreen(buf: ArrayBuffer): ScreenMessage | null {
  const b = new Uint8Array(buf);
  if (b.length < 1) return null;
  const dv = new DataView(buf);
  if (b[0] === ScreenMsg.Info) {
    if (b.length < 7) return null;
    return {
      kind: "info",
      monitorIndex: dv.getUint16(1, true),
      width: dv.getUint16(3, true),
      height: dv.getUint16(5, true),
    };
  }
  if (b[0] === ScreenMsg.Tile) {
    if (b.length < 11) return null;
    return {
      kind: "tile",
      x: dv.getUint16(1, true),
      y: dv.getUint16(3, true),
      w: dv.getUint16(5, true),
      h: dv.getUint16(7, true),
      codec: b[9],
      data: b.subarray(11),
    };
  }
  return null;
}

// mimeForCodec maps a tile codec byte to a Blob MIME type.
export function mimeForCodec(codec: number): string {
  return codec === 0 ? "image/jpeg" : "application/octet-stream";
}
