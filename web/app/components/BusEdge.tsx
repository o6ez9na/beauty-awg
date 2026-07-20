"use client";

import { useEffect, useMemo, useRef } from "react";
import { BaseEdge, getStraightPath, useInternalNode, type EdgeProps, type InternalNode, type Node } from "@xyflow/react";

// A bus joins two locations that reach each other both ways. Routing it through
// the fixed right/left handles sends it on a long detour around the outside
// whenever the two cards are stacked vertically, which is most of the time.
//
// So it is a floating edge instead: the line runs straight between the two
// cards' centres and is clipped where it crosses each border. Whatever the
// relative positions, it takes the short way through the middle.
//
// Traffic is shown as loose particles rather than a dashed stroke. A dash
// pattern is periodic by construction: every bead is the same size, evenly
// spaced, at one speed, and the pattern visibly restarts.
//
// These are simulated per frame instead. The important part is that a particle
// does NOT run end-to-end: it spawns at a random point along the line, lives for
// a random span, and fades out wherever it happens to be. Travelling the full
// length would put every birth and death at the two ends, and those fixed spots
// are exactly what reads as "the animation starting again". With lifetimes,
// there is no point on the line where things appear.

function borderPoint(from: InternalNode<Node>, to: InternalNode<Node>) {
  const fw = (from.measured.width ?? 0) / 2;
  const fh = (from.measured.height ?? 0) / 2;
  const fx = from.internals.positionAbsolute.x + fw;
  const fy = from.internals.positionAbsolute.y + fh;
  const tx = to.internals.positionAbsolute.x + (to.measured.width ?? 0) / 2;
  const ty = to.internals.positionAbsolute.y + (to.measured.height ?? 0) / 2;

  if (fw === 0 || fh === 0) return { x: fx, y: fy };

  // Normalise the centre-to-centre vector onto the unit square, then scale it
  // back out to the rectangle: the result is where the line leaves the border.
  const dx = (tx - fx) / (2 * fw);
  const dy = (ty - fy) / (2 * fh);
  const u = dx - dy;
  const v = dx + dy;
  const scale = 1 / (Math.abs(u) + Math.abs(v) || 1);
  const su = scale * u;
  const sv = scale * v;

  return { x: fw * (su + sv) + fx, y: fh * (sv - su) + fy };
}

type BusData = { highlight?: string; dim?: boolean };

type Particle = {
  t: number;      // position along the line, 0..1
  speed: number;  // signed: the link is mutual, so some run backwards
  amp: number; wf: number; phase: number;    // sideways drift, slow wave
  amp2: number; wf2: number; phase2: number; // and a faster, smaller one
  r: number; rf: number; alpha: number;
  life: number;   // 0..1 through this particle's own lifetime
  dur: number;    // how many seconds that lifetime lasts
};

// A fixed pool is rendered; how many are actually flown depends on how long the
// bus is, so a short hop isn't crowded and a long one isn't sparse.
const POOL = 34;
const PER_PX = 1 / 24;
const MIN_LIVE = 9;

// Seeded so a given bus always gets the same swarm: re-renders (every poll)
// must not reshuffle the particles under the user.
function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Give a particle a fresh set of properties and drop it somewhere new. Called
 *  at birth and every time one dies, so the swarm never settles into a loop. */
function respawn(p: Particle, rnd: () => number, fresh: boolean) {
  p.t = rnd();
  p.speed = (0.03 + rnd() * 0.12) * (rnd() < 0.5 ? -1 : 1);
  p.amp = 1 + rnd() * 5;
  p.wf = 0.4 + rnd() * 1.5;
  p.phase = rnd() * Math.PI * 2;
  p.amp2 = 0.5 + rnd() * 2;
  p.wf2 = 2 + rnd() * 3.6;
  p.phase2 = rnd() * Math.PI * 2;
  p.r = 1.3 + rnd() * 2;
  p.rf = 1 + rnd() * 2.5;
  p.alpha = 0.5 + rnd() * 0.5;
  p.dur = 2.6 + rnd() * 5.5;
  // Start mid-life on the first frame, otherwise the whole swarm fades up
  // together the moment the map opens.
  p.life = fresh ? rnd() : 0;
}

type Swarm = { particles: Particle[]; rnd: () => number };

// Kept OUTSIDE the component, keyed by edge id. React Flow tears the edge's DOM
// down and rebuilds it whenever the edges array is replaced, which remounts this
// component; per-instance state would be recreated and every particle would
// snap back to nothing. Module state survives that, so the swarm carries on
// exactly where it was.
const SWARMS = new Map<string, Swarm>();

function getSwarm(id: string, seed: number): Swarm {
  const existing = SWARMS.get(id);
  if (existing) return existing;
  // Bounded: edges come and go over a long session.
  if (SWARMS.size > 64) SWARMS.clear();
  const rnd = mulberry32(seed);
  const particles = Array.from({ length: POOL }, () => {
    const p = {} as Particle;
    respawn(p, rnd, true);
    return p;
  });
  const swarm = { particles, rnd };
  SWARMS.set(id, swarm);
  return swarm;
}

type Geom = { ax: number; ay: number; bx: number; by: number };

/** Where a particle is right now. Shared by the animation loop and the render
 *  itself, so the first painted frame after a remount is already correct
 *  instead of a blank one. */
function place(p: Particle, g: Geom, clock: number) {
  const dx = g.bx - g.ax;
  const dy = g.by - g.ay;
  const len = Math.hypot(dx, dy) || 1;
  const wobble =
    Math.sin(clock * p.wf + p.phase) * p.amp + Math.sin(clock * p.wf2 + p.phase2) * p.amp2;
  return {
    cx: g.ax + dx * p.t + (-dy / len) * wobble,
    cy: g.ay + dy * p.t + (dx / len) * wobble,
    r: p.r * (0.8 + 0.2 * Math.sin(clock * p.rf + p.phase)),
    opacity: lifeFade(p.life) * edgeFade(p.t) * p.alpha,
  };
}

/** Fade in after birth, fade out towards death. */
function lifeFade(life: number) {
  const inn = 0.18;
  const out = 0.3;
  const e = life < inn ? life / inn : life > 1 - out ? (1 - life) / out : 1;
  return e * e * (3 - 2 * e); // smoothstep
}

/** 0 at the very ends of the line, 1 across the middle: keeps particles from
 *  drawing on top of the cards. */
function edgeFade(t: number) {
  const edge = 0.1;
  if (t <= 0 || t >= 1) return 0;
  const e = t < edge ? t / edge : t > 1 - edge ? (1 - t) / edge : 1;
  return e * e * (3 - 2 * e);
}

function hash(s: string) {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export default function BusEdge({ id, source, target, data, style, interactionWidth }: EdgeProps) {
  const from = useInternalNode(source);
  const to = useInternalNode(target);

  const geom = useRef({ ax: 0, ay: 0, bx: 0, by: 0 });
  const swarmRef = useRef<SVGGElement>(null);
  const { particles, rnd } = useMemo(() => getSwarm(id, hash(id)), [id]);

  const d = (data ?? {}) as BusData;
  const active = !d.dim && !!from && !!to;

  useEffect(() => {
    if (!active) return;
    if (typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let raf = 0;
    let last = performance.now();
    const step = (now: number) => {
      // Clamped: a backgrounded tab resumes with a huge delta, which would
      // teleport every particle.
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;

      const g = geom.current;
      const len = Math.hypot(g.bx - g.ax, g.by - g.ay) || 1;
      const clock = now / 1000;
      const kids = swarmRef.current?.childNodes;
      const live = Math.max(MIN_LIVE, Math.min(POOL, Math.round(len * PER_PX)));

      for (let i = 0; i < particles.length; i++) {
        const el = kids?.[i] as SVGCircleElement | undefined;
        if (!el) continue;
        if (i >= live) { el.setAttribute("opacity", "0"); continue; }

        const p = particles[i];
        p.life += dt / p.dur;
        if (p.life >= 1) respawn(p, rnd, false);
        p.t += p.speed * dt;
        // A long-lived particle that reaches an end wraps, but it is edge-faded
        // to nothing by then, so the jump is never seen.
        if (p.t > 1) p.t -= 1;
        else if (p.t < 0) p.t += 1;

        const at = place(p, g, clock);
        el.setAttribute("cx", String(at.cx));
        el.setAttribute("cy", String(at.cy));
        el.setAttribute("r", String(at.r));
        el.setAttribute("opacity", String(at.opacity));
      }
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [active, particles, rnd]);

  if (!from || !to) return null;

  const a = borderPoint(from, to);
  const b = borderPoint(to, from);
  geom.current = { ax: a.x, ay: a.y, bx: b.x, by: b.y };
  const [path] = getStraightPath({ sourceX: a.x, sourceY: a.y, targetX: b.x, targetY: b.y });

  const width = Number(style?.strokeWidth ?? 7);
  const accent = String(style?.stroke ?? "currentColor");
  const core = Math.max(1.6, width / 3.2);

  // Faded buses are context, not content: no halo, no swarm.
  if (d.dim) {
    return <BaseEdge id={id} path={path} style={style} interactionWidth={interactionWidth ?? 24} />;
  }

  return (
    <>
      <path className="bus-halo" d={path} style={{ stroke: accent, strokeWidth: width + 9 }} />
      <BaseEdge id={id} path={path} style={style} interactionWidth={interactionWidth ?? 24} />
      <path className="bus-core" d={path} style={{ stroke: d.highlight, strokeWidth: core }} />
      <g ref={swarmRef} className="bus-swarm" fill={d.highlight}>
        {particles.map((p, i) => {
          // Painted from live particle state, so a remount picks up mid-flight.
          const at = place(p, geom.current, (typeof performance !== "undefined" ? performance.now() : 0) / 1000);
          return <circle key={i} cx={at.cx} cy={at.cy} r={at.r} opacity={at.opacity} />;
        })}
      </g>
    </>
  );
}
