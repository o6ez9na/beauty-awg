// A node's identity is its HUE; saturation and lightness come from the theme.
// Storing only the hue as identity is what lets the same device look like a
// pale chip on the white canvas and a deep one on the graphite canvas — pastels
// that read as calm on white are glaring on dark, and a single fixed band
// cannot serve both. readableTextColor then picks black/white text for whatever
// the band produced, so contrast holds either way.

export type ThemeName = "light" | "dark";
type Band = { sat: number; light: number };

const BANDS: Record<ThemeName, Band> = {
  // LIGHT is high enough that even the darkest-perceived hue (blue, ~240deg)
  // still clears readableTextColor's threshold, so every card lands on dark text.
  light: { sat: 40, light: 74 },
  // Deep and muted: on graphite these read as solid chips rather than the
  // eye-watering pastel blocks a light-theme band produces.
  dark: { sat: 50, light: 28 },
};

// Small dots (access chips, the wizard's pick list) sit on both white and
// graphite surfaces, so they get one mid band that is visible against either.
const MARK: Band = { sat: 52, light: 55 };

// The canonical band a picked color is stored in. Rendering re-derives the hue
// from it, so the stored value stays stable no matter which theme picked it.
const SAT = BANDS.light.sat;
const LIGHT = BANDS.light.light;

// FNV-1a: better bit avalanche than a plain polynomial hash, so addresses
// that differ by one character (e.g. neighboring IPs) don't land on
// near-identical hues.
function fnv1a(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0; // unsigned 32-bit
}

// The golden angle (360° * (1 - 1/φ)) spreads hues drawn from arbitrary hash
// values roughly evenly around the wheel instead of clustering — the same
// trick used to place points evenly without a lookup table.
const GOLDEN_ANGLE = 137.50776405003785;

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
  const h = fnv1a(seed) * GOLDEN_ANGLE;
  return h % 360;
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

type Colorable = { color: string; address: string; id: string };

/** A node's identifying hue: taken from a stored override if there is one, else
 * derived from its address (falling back to id for nodes without one yet). */
export function hueOfNode(node: Colorable): number {
  return node.color ? hueFromHex(node.color) : hueOf(node.address || node.id);
}

/** The color to actually render a card in, for the theme on screen. */
export function nodeColor(node: Colorable, theme: ThemeName = "light"): string {
  const b = BANDS[theme];
  return hslToHex(hueOfNode(node), b.sat, b.light);
}

/** Same identity, sized for a small dot on any surface. */
export function markColor(node: Colorable): string {
  return hslToHex(hueOfNode(node), MARK.sat, MARK.light);
}

/** The node's own color, pushed toward contrast so the connection point reads
 * as part of the card but distinct from it. On dark cards that means lighter;
 * on the light theme's pale cards, lighter would vanish into the canvas, so it
 * goes the other way. Either way it stays the same hue. */
export function handleColor(node: Colorable, theme: ThemeName = "light"): string {
  const b = BANDS[theme];
  const light = theme === "dark" ? Math.min(96, b.light + 18) : Math.max(4, b.light - 18);
  return hslToHex(hueOfNode(node), b.sat, light);
}

/** The band a given theme renders cards in — for previewing a pick. */
export function bandFor(theme: ThemeName): Band {
  return BANDS[theme];
}

export const PICKER_SAT = SAT;
export const PICKER_LIGHT = LIGHT;
