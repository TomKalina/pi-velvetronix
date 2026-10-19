// Shared test helpers and protocol.md §11 vectors (test-only module).

export function hex(s: string): Uint8Array {
  const parts = s.trim().split(/\s+/).filter(Boolean);
  return Uint8Array.from(parts.map((b) => {
    if (!/^[0-9a-fA-F]{2}$/.test(b)) throw new Error(`bad hex byte ${b}`);
    return parseInt(b, 16);
  }));
}

export function toHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).toUpperCase().padStart(2, "0")).join(" ");
}

/** Wraps a §11.1 payload (bytes after F0 42, before F7). */
export function payloadFrame(payload: string): Uint8Array {
  return Uint8Array.from([0xf0, 0x42, ...hex(payload), 0xf7]);
}

// §11.4 vector A
export const VECTOR_A_STRUCT = hex(`
50 49 20 54 45 53 54 20 20 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
80 00 32 32 32 00 01 05 FE 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00`);

export const VECTOR_A_FRAME = hex(`
F0 42 30 00 01 34 4C 00 03 00 50 49 20 54 45 53 54 00 20 20 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00 10 01 02 01 01 00 00 32 20 32 32 00 01 05 7E 00 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 00 01 00 32 32 14 32 32 00 F7`);

export const VECTOR_A_PACKED = VECTOR_A_FRAME.subarray(9, 80);

// §11.5 vector B
export const VECTOR_B_STRUCT = hex(`
44 45 4C 41 59 20 4D 41 58 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
10 27 43 39 0A 00 00 05 B0 04 32 4D 32 01 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00`);

export const VECTOR_B_PACKED = hex(`
00 44 45 4C 41 59 20 4D 00 41 58 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00
00 01 02 01 01 10 27 43 20 39 0A 00 00 05 30 04 00 32 4D 32 01 00 00 00 00 00 00 00 00 00 00 01
00 32 32 14 32 32 00`);

/** Current-program (40) frame carrying the given packed block. */
export function currentFrame(packed: Uint8Array): Uint8Array {
  return Uint8Array.from([0xf0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x40, ...packed, 0xf7]);
}

/** Program dump (4C 00 pp) frame carrying the given packed block. */
export function programFrame(slot: number, packed: Uint8Array, bank = 0): Uint8Array {
  return Uint8Array.from([0xf0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x4c, bank, slot, ...packed, 0xf7]);
}
