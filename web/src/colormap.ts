// Turbo colormap (polynomial approximation by Anton Mikhailov), 256 entries, RGB.
function clamp01(x: number): number {
  return x < 0 ? 0 : x > 1 ? 1 : x;
}

function turbo(t: number): [number, number, number] {
  const r =
    0.13572138 + t * (4.6153926 + t * (-42.66032258 + t * (132.13108234 + t * (-152.54893924 + t * 59.28637943))));
  const g =
    0.09140261 + t * (2.19418839 + t * (4.84296658 + t * (-14.18503333 + t * (4.27729857 + t * 2.82956604))));
  const b =
    0.1066733 + t * (12.64194608 + t * (-60.58204836 + t * (110.36276771 + t * (-89.90310912 + t * 27.34824973))));
  return [clamp01(r) * 255, clamp01(g) * 255, clamp01(b) * 255];
}

/** 256 entries, packed little-endian RGBA (0xAABBGGRR) for Uint32Array writes into ImageData. */
export const LUT32: Uint32Array = (() => {
  const out = new Uint32Array(256);
  for (let i = 0; i < 256; i++) {
    const [r, g, b] = turbo(i / 255);
    out[i] = (255 << 24) | (Math.round(b) << 16) | (Math.round(g) << 8) | Math.round(r);
  }
  return out;
})();
