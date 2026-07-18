// Deterministic per-node accent color. Saturation and lightness are fixed (the
// same values the hue slider in ColorPickerModal previews against), so every
// node — auto or user-picked — sits in the same readable band; only hue
// varies. readableTextColor then picks black/white text for whatever color
// actually lands there, so contrast holds even if a stored override strays
// outside that band.

const SAT = 58;
const LIGHT = 46;

function hashString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return h;
}

export function hslToHex(h: number, s: number, l: number): string {
  s /= 100;
  l /= 100;
  const k = (n: number) => (n + h / 30) % 12;
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)));
  const toHex = (v: number) => Math.round(v * 255).toString(16).padStart(2, "0");
  return "#" + toHex(f(0)) + toHex(f(8)) + toHex(f(4));
}

function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** Hue (0-359) a node's auto-color derives from, for seeding the picker. */
export function hueOf(seed: string): number {
  return ((hashString(seed) % 360) + 360) % 360;
}

/** Hue (0-359) of an arbitrary "#rrggbb", for seeding the picker slider from a
 * previously stored custom color. */
export function hueFromHex(hex: string): number {
  const rgb = hexToRgb(hex);
  if (!rgb) return 0;
  const [r, g, b] = rgb.map((v) => v / 255);
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  if (max === min) return 0;
  const d = max - min;
  let h: number;
  switch (max) {
    case r: h = ((g - b) / d) % 6; break;
    case g: h = (b - r) / d + 2; break;
    default: h = (r - g) / d + 4;
  }
  h *= 60;
  return h < 0 ? h + 360 : h;
}

/** Deterministic accent color for a node with no explicit override. */
export function defaultNodeColor(seed: string): string {
  return hslToHex(hueOf(seed), SAT, LIGHT);
}

/** "#15181f" or "#fff" — whichever reads better on top of `bg`. */
export function readableTextColor(bg: string): string {
  const rgb = hexToRgb(bg);
  if (!rgb) return "#fff";
  const [r, g, b] = rgb;
  const luminance = 0.299 * r + 0.587 * g + 0.114 * b;
  return luminance > 150 ? "#15181f" : "#fff";
}

/** The color to actually render: a stored override, or the deterministic
 * default derived from the node's address (falling back to id for nodes
 * without one yet, e.g. still pending). */
export function nodeColor(node: { color: string; address: string; id: string }): string {
  return node.color || defaultNodeColor(node.address || node.id);
}

export const PICKER_SAT = SAT;
export const PICKER_LIGHT = LIGHT;
