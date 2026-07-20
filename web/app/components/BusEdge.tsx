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

// A bus doesn't just appear or vanish: forming one plays the two crossing
// arrows folding into a single trunk, and splitting one back to a one-way link
// plays the trunk thinning out and bending into a single curved line. The
// timeline is driven here from a start timestamp; AccessGraph decides WHEN a
// transition begins and holds the bus on-screen for a split's duration.
type BusTransition = { kind: "form" | "split"; start: number };
type BusData = { highlight?: string; dim?: boolean; transition?: BusTransition };

const T_FORM = 760;   // ms: eight -> straight -> bus
const T_SPLIT = 680;  // ms: bus -> straight -> single curved line

function smooth(e: number) {
  const c = e < 0 ? 0 : e > 1 ? 1 : e;
  return c * c * (3 - 2 * c);
}

// `m` is the shape blend: 0 = the exact bezier a one-way link draws between the
// node handles, 1 = the straight border-to-border line the bus rides. Animating
// `m` (plus the anchors and the control offset together) means the transition
// begins and ends on the very lines it replaces — no jump from the handles to
// the card centres.
type Phase = { m: number; body2Op: number; width: number; busAlpha: number; done: boolean };

// Where the transition sits at time `now`. `target` is the settled bus width.
function busPhase(t: BusTransition, now: number, target: number): Phase {
  const T = t.kind === "form" ? T_FORM : T_SPLIT;
  const p = Math.min(1, Math.max(0, (now - t.start) / T));
  const thin = 2;

  if (t.kind === "form") {
    // The two handle beziers straighten onto the trunk line first, then the flat
    // line fills out into the bus over an overlapping second stretch.
    const collapse = smooth(p / 0.45);
    const expand = smooth((p - 0.4) / 0.6);
    return {
      m: collapse,
      body2Op: 1 - collapse,
      width: thin + (target - thin) * expand,
      busAlpha: expand,
      done: p >= 1,
    };
  }
  // split: the trunk thins to the flat line, then that line bends back out into
  // the exact bezier the surviving one-way link takes over.
  const shrink = smooth(p / 0.6);
  const straighten = smooth((p - 0.35) / 0.65);
  return {
    m: 1 - straighten,
    body2Op: 0,
    width: target - (target - thin) * shrink,
    busAlpha: 1 - shrink,
    done: p >= 1,
  };
}

type Pt = { x: number; y: number };

// The right-hand (source) and left-hand (target) handle anchors of a card —
// where React Flow's default node->node edge actually attaches.
function handles(n: InternalNode<Node>) {
  const w = n.measured.width ?? 0;
  const h = n.measured.height ?? 0;
  const x = n.internals.positionAbsolute.x;
  const y = n.internals.positionAbsolute.y;
  return { right: { x: x + w, y: y + h / 2 }, left: { x, y: y + h / 2 } };
}

// React Flow's own bezier control offset (curvature 0.25), so at m=0 the path is
// pixel-identical to the link edge it hands off to.
function ctrlOffset(dx: number) {
  return dx >= 0 ? 0.5 * dx : 0.25 * 25 * Math.sqrt(-dx);
}
function lerp(a: number, b: number, t: number) {
  return a + (b - a) * t;
}

// Blend a handle-anchored bezier (m=0) into a straight border-to-border line
// (m=1): endpoints slide from handle to border, and the control arms retract to
// nothing, together.
function morphPath(h0: Pt, h3: Pt, b0: Pt, b3: Pt, m: number) {
  const x0 = lerp(h0.x, b0.x, m), y0 = lerp(h0.y, b0.y, m);
  const x3 = lerp(h3.x, b3.x, m), y3 = lerp(h3.y, b3.y, m);
  const off = ctrlOffset(x3 - x0) * (1 - m);
  return `M ${x0} ${y0} C ${x0 + off} ${y0} ${x3 - off} ${y3} ${x3} ${y3}`;
}

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
  // Handle anchors of the two cards, refreshed every render so the transition
  // loop can read live positions (a card can be dragged mid-animation).
  const morphRef = useRef({
    fromR: { x: 0, y: 0 }, toL: { x: 0, y: 0 },
    toR: { x: 0, y: 0 }, fromL: { x: 0, y: 0 },
  });
  const swarmRef = useRef<SVGGElement>(null);
  // Transitional layers, driven imperatively per frame like the swarm so a
  // form/split plays smoothly without re-rendering the whole edge each tick.
  const haloRef = useRef<SVGPathElement>(null);
  const bodyRef = useRef<SVGPathElement>(null);
  const body2Ref = useRef<SVGPathElement>(null);
  const coreRef = useRef<SVGPathElement>(null);
  const { particles, rnd } = useMemo(() => getSwarm(id, hash(id)), [id]);

  const d = (data ?? {}) as BusData;
  const active = !d.dim && !!from && !!to;
  const trans = d.transition;
  const busWidth = Number(style?.strokeWidth ?? 7);
  const accent = String(style?.stroke ?? "currentColor");
  // Re-run the loop when a transition begins or ends, so `trans` is never stale.
  const transSig = trans ? `${trans.kind}:${trans.start}` : "";

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

      // Fold/unfold the trunk when a transition is running; otherwise the bus is
      // fully formed and the swarm rides at full strength.
      const ph = trans ? busPhase(trans, now, busWidth) : null;
      const busAlpha = ph ? ph.busAlpha : 1;
      if (ph) {
        // Width and opacity must go through `style`, not setAttribute: the JSX
        // seeds them as inline styles, and a CSS property always wins over the
        // matching presentation attribute — an attribute update would be ignored.
        const mo = morphRef.current;
        const bFrom = { x: g.ax, y: g.ay };
        const bTo = { x: g.bx, y: g.by };
        const bodyD = morphPath(mo.fromR, mo.toL, bFrom, bTo, ph.m);
        const halo = haloRef.current;
        if (halo) {
          halo.setAttribute("d", bodyD);
          halo.style.strokeWidth = `${ph.width + 9}px`;
          halo.style.opacity = String(0.18 * busAlpha);
        }
        const body = bodyRef.current;
        if (body) {
          body.setAttribute("d", bodyD);
          body.style.strokeWidth = `${ph.width}px`;
        }
        const body2 = body2Ref.current;
        if (body2) {
          body2.setAttribute("d", morphPath(mo.toR, mo.fromL, bTo, bFrom, ph.m));
          body2.style.strokeWidth = `${ph.width}px`;
          body2.style.opacity = String(ph.body2Op);
        }
        const coreEl = coreRef.current;
        if (coreEl) {
          coreEl.setAttribute("d", bodyD);
          coreEl.style.strokeWidth = `${Math.max(1.6, ph.width / 3.2)}px`;
          coreEl.style.opacity = String(0.2 * busAlpha);
        }
      }

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
        el.setAttribute("opacity", String(at.opacity * busAlpha));
      }
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [active, particles, rnd, trans, busWidth, transSig]);

  if (!from || !to) return null;

  const a = borderPoint(from, to);
  const b = borderPoint(to, from);
  geom.current = { ax: a.x, ay: a.y, bx: b.x, by: b.y };
  const Hf = handles(from);
  const Ht = handles(to);
  morphRef.current = { fromR: Hf.right, toL: Ht.left, toR: Ht.right, fromL: Hf.left };
  const [path] = getStraightPath({ sourceX: a.x, sourceY: a.y, targetX: b.x, targetY: b.y });

  const width = busWidth;
  const core = Math.max(1.6, width / 3.2);

  // Faded buses are context, not content: no halo, no swarm.
  if (d.dim) {
    return <BaseEdge id={id} path={path} style={style} interactionWidth={interactionWidth ?? 24} />;
  }

  // Mid transition: the visible trunk is hand-drawn (updated per frame), while
  // the BaseEdge stays as a straight, invisible hit area for clicks.
  if (trans) {
    const now0 = typeof performance !== "undefined" ? performance.now() : 0;
    const ph = busPhase(trans, now0, width);
    const bodyD0 = morphPath(Hf.right, Ht.left, a, b, ph.m);
    const body2D0 = morphPath(Ht.right, Hf.left, b, a, ph.m);
    const bodyStyle = { fill: "none", strokeLinecap: "round" as const };
    return (
      <>
        <path ref={haloRef} d={bodyD0} style={{ ...bodyStyle, stroke: accent, strokeWidth: ph.width + 9, opacity: 0.18 * ph.busAlpha }} />
        <path ref={body2Ref} d={body2D0} style={{ ...bodyStyle, stroke: accent, strokeWidth: ph.width, opacity: ph.body2Op }} />
        <path ref={bodyRef} d={bodyD0} style={{ ...bodyStyle, stroke: accent, strokeWidth: ph.width }} />
        <BaseEdge id={id} path={path} style={{ ...style, stroke: "transparent" }} interactionWidth={interactionWidth ?? 24} />
        <path ref={coreRef} d={bodyD0} style={{ ...bodyStyle, stroke: d.highlight, strokeWidth: Math.max(1.6, ph.width / 3.2), opacity: 0.2 * ph.busAlpha }} />
        <g ref={swarmRef} className="bus-swarm" fill={d.highlight}>
          {particles.map((p, i) => {
            const at = place(p, geom.current, now0 / 1000);
            return <circle key={i} cx={at.cx} cy={at.cy} r={at.r} opacity={at.opacity * ph.busAlpha} />;
          })}
        </g>
      </>
    );
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
