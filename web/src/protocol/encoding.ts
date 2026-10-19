// Value encodings, docs/protocol.md §2.

/** Live 14-bit value as [lsb, msb], 7 bits each. */
export function enc14(v: number): [number, number] {
  if (!Number.isInteger(v) || v < 0 || v > 0x3fff) throw new RangeError(`14-bit value out of range: ${v}`);
  return [v & 0x7f, (v >> 7) & 0x7f];
}

export function dec14(lo: number, hi: number): number {
  return (lo & 0x7f) | ((hi & 0x7f) << 7);
}

/** KORG 7-to-8 packing: each set of up to 7 bytes becomes an MSB byte followed by the 7-bit bytes (62 -> 71). */
export function pack7(data: Uint8Array): Uint8Array {
  const out: number[] = [];
  for (let i = 0; i < data.length; i += 7) {
    const set = data.subarray(i, i + 7);
    let msb = 0;
    set.forEach((b, j) => {
      if (b & 0x80) msb |= 1 << j;
    });
    out.push(msb, ...Array.from(set, (b) => b & 0x7f));
  }
  return Uint8Array.from(out);
}

/** Inverse of pack7 (71 -> 62). Throws on bytes >= 0x80 or MSB bits for absent data bytes. */
export function unpack7(p: Uint8Array): Uint8Array {
  const out: number[] = [];
  for (let i = 0; i < p.length; i += 8) {
    const msb = p[i];
    const n = Math.min(7, p.length - i - 1);
    if (n === 0) throw new Error(`packed data ends with a lone MSB byte at ${i}`);
    if (msb & 0x80 || msb >> n !== 0) throw new Error(`bad MSB byte 0x${msb.toString(16)} at ${i}`);
    for (let j = 0; j < n; j++) {
      const b = p[i + 1 + j];
      if (b & 0x80) throw new Error(`8-bit byte 0x${b.toString(16)} in packed data at ${i + 1 + j}`);
      out.push(b | (((msb >> j) & 1) << 7));
    }
  }
  return Uint8Array.from(out);
}
