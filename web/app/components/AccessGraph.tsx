"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  Position,
  addEdge,
  useNodesState,
  useEdgesState,
  MarkerType,
  type Node as RFNode,
  type Edge as RFEdge,
  type Connection,
  type NodeProps,
  type XYPosition,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api, Node as VpnNode, Client, NodeLink } from "../lib/api";
import RulesModal from "./RulesModal";
import AddDeviceWizard from "./AddDeviceWizard";
import ClientDetails from "./ClientDetails";
import RenameModal from "./RenameModal";
import LinkModal from "./LinkModal";
import { handleColor, markColor, nodeColor, readableTextColor } from "../lib/color";
import { configChanged, toast } from "../lib/toast";
import { humanError } from "../lib/errors";
import { useTheme } from "../lib/theme";
import {
  Group, GROUP_DEF_H, GROUP_DEF_W, Layout, gid, isGroupId, groupGrants,
  groupedClientIds, memberSeat, newGroupId, parseLayout, serializeLayout, syncPlan,
} from "../lib/groups";
import GroupNode from "./GroupNode";
import BusEdge from "./BusEdge";
import GroupModal from "./GroupModal";

// site-to-site (node->node) edge accent, distinct from the blue grant edges.
const LINK_COLOR = "#f0a020";
// Everything the current selection doesn't touch drops to this, so the lines
// that matter read against a quiet background rather than disappearing. The
// graph paints edges and the dot grid inline, so both need the resolved theme.
const FADED = { dark: "#423b36", light: "#c3ccd8" } as const;
const GRID = { dark: "#302b27", light: "#dfe4ea" } as const;
const HINT_KEY = "6ers3rk-graph-hint";
const linkEdgeId = (src: string, dst: string) => `lnk:${src}~${dst}`;
const isLinkEdge = (id: string) => id.startsWith("lnk:");
const parseLinkEdge = (id: string) => id.slice(4).split("~"); // [src, dst]

// Two locations linked BOTH ways are drawn as one thick bus rather than a pair
// of arrows crossing over each other. Ids are sorted so the same pair always
// produces the same edge regardless of which direction was added first.
const busEdgeId = (a: string, b: string) => `bus:${[a, b].sort().join("~")}`;
const isBusEdge = (id: string) => id.startsWith("bus:");
const parseBusEdge = (id: string) => id.slice(4).split("~"); // [a, b]
const BUS_WIDTH = 7;
// How long a bus takes to fold together / collapse apart. Kept in step with the
// same timings inside BusEdge, which draws the actual frames.
const FORM_MS = 760;
const SPLIT_MS = 680;
// Lighter along the top of the bundle in the dark theme, darker in the light
// one — either way it reads as a highlight running down the trunk.
const BUS_CORE = { dark: "#ffffff", light: "#14202b" } as const;

const cid = (id: string) => `c:${id}`;
const nid = (id: string) => `n:${id}`;
const unwrap = (rfId: string) => rfId.slice(2);
// Groups have no hue of their own — they are a container, not a peer.
const GROUP_COLOR = { dark: "#3a3430", light: "#e8ecf2" } as const;

type PosMap = Record<string, XYPosition>;

function Dot({ online }: { online?: boolean }) {
  return <span className={"dot " + (online ? "live" : "")} style={{ marginRight: 7 }} />;
}

// Shared card data. `sel` is the node the user clicked, `dim` is everything the
// selection pushed to the back.
type CardData = {
  label: string;
  sub: string;
  sel?: boolean;
  dim?: boolean;
  /* a legal drop target for the connection currently being dragged */
  candidate?: boolean;
  /* dragging a connection that can't land here */
  muted?: boolean;
  /* the card this connection is being dragged from */
  dragging?: boolean;
  hub?: boolean;
  online?: boolean;
  color?: string;
  textColor?: string;
  /* the card's own hue, shifted for the connection points */
  accent?: string;
  /* sits inside a group, which owns the line out */
  inside?: boolean;
  onGear?: () => void;
};

// --node-accent feeds the connection-point pseudo-element in CSS, which can't
// reach a per-node value any other way.
const cardStyle = (d: CardData) =>
  ({
    background: d.color,
    color: d.textColor,
    borderColor: "transparent",
    "--node-accent": d.accent,
  }) as React.CSSProperties;

// The gear only exists on the selected card: clicking a card is how you pick it
// out of the mesh, so opening a window has to be a separate, deliberate target.
function Gear({ d, label }: { d: CardData; label: string }) {
  if (!d.sel || !d.onGear) return null;
  return (
    <button
      className="gnode-gear nodrag nopan"
      aria-label={label}
      title={label}
      onClick={(e) => { e.stopPropagation(); d.onGear?.(); }}
      onMouseDown={(e) => e.stopPropagation()}
    >
      ⚙
    </button>
  );
}

// Color is set from the Devices/Locations lists, not here — the graph only
// displays it, keeping the card free of controls until it's selected.
function ClientNodeView({ data }: NodeProps) {
  const d = data as CardData;
  return (
    <div
      className={
        "gnode gnode-client" + (d.sel ? " sel" : "") + (d.dim ? " dim" : "") +
        (d.candidate ? " candidate" : "") + (d.muted ? " muted" : "") +
        (d.dragging ? " dragging" : "") + (d.inside ? " inside" : "")
      }
      style={cardStyle(d)}
    >
      <div className="gnode-title"><Dot online={d.online} />{d.label}</div>
      <div className="gnode-sub" style={{ color: "inherit", opacity: 0.85 }}>{d.sub}</div>
      <Gear d={d} label={`Open settings for ${d.label}`} />
      {!d.inside && <Handle type="source" position={Position.Right} />}
    </div>
  );
}

function ServerNodeView({ data }: NodeProps) {
  const d = data as CardData;
  return (
    <div
      className={
        "gnode " + (d.hub ? "gnode-hub" : "gnode-server") + (d.sel ? " sel" : "") +
        (d.dim ? " dim" : "") + (d.candidate ? " candidate" : "") + (d.muted ? " muted" : "") +
        (d.dragging ? " dragging" : "")
      }
      style={cardStyle(d)}
    >
      <Handle type="target" position={Position.Left} />
      <div className="gnode-title">{d.hub ? "◍ " : <Dot online={d.online} />}{d.label}</div>
      <div className="gnode-sub" style={{ color: "inherit", opacity: 0.85 }}>{d.sub}</div>
      <Gear d={d} label={`Rename ${d.label}`} />
      {/* real nodes can also be a link SOURCE (drag from the right to another node) */}
      {!d.hub && <Handle type="source" position={Position.Right} />}
    </div>
  );
}

// screenToFlowPosition needs the React Flow context, and the context only
// exists below <ReactFlow>. Wrapping here lets the graph body use the hook.
export default function AccessGraph(props: GraphProps) {
  return (
    <ReactFlowProvider>
      <Graph {...props} />
    </ReactFlowProvider>
  );
}

type GraphProps = {
  nodes: VpnNode[];
  clients: Client[];
  links: NodeLink[];
  onChanged: () => void;
  onError?: (msg: string) => void;
};

function Graph({
  nodes: vpnNodes,
  clients,
  links,
  onChanged,
  onError,
}: GraphProps) {
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<RFNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [editing, setEditing] = useState<{ clientId: string; nodeId: string } | null>(null);
  const [editingLink, setEditingLink] = useState<{ src: string; dst: string } | null>(null);
  const [detailsClientId, setDetailsClientId] = useState<string | null>(null);
  const [renamingNode, setRenamingNode] = useState<{ id: string; name: string } | null>(null);
  const [ready, setReady] = useState(false);
  // The clicked card, as a React Flow id ("c:<uuid>" / "n:<uuid>"). Selecting is
  // how you read one device's reach out of a mesh that is otherwise a hairball.
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Signature of the last edge set handed to React Flow.
  const edgeSig = useRef("");
  const nodeSig = useRef("");
  // Mirrors the committed nodes so the reconcile below can read current
  // positions without a state updater — updaters must stay pure, and React
  // invokes them twice in development.
  const rfNodesRef = useRef<RFNode[]>([]);
  const [hintOpen, setHintOpen] = useState(true);
  // The handle a connection is being dragged from, while the drag is in flight.
  const [connectingFrom, setConnectingFrom] = useState<string | null>(null);
  // Right-click on empty canvas: add a device where you clicked.
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [addingDevice, setAddingDevice] = useState(false);
  const dropAt = useRef<XYPosition | null>(null);
  const [groups, setGroups] = useState<Group[]>([]);
  const [editingGroup, setEditingGroup] = useState<string | null>(null);
  // Rules for a whole group: same editor, applied to every member.
  const [editingGroupRules, setEditingGroupRules] = useState<{ groupId: string; nodeId: string } | null>(null);
  // Mirrors `groups` so the debounced position save can read the current value
  // without being torn down and rebuilt on every membership change.
  const groupsRef = useRef<Group[]>([]);
  // The node builder runs before updateGroup is declared, so it reaches it here.
  const updateGroupRef = useRef<((id: string, patch: Partial<Group>) => void) | null>(null);
  const { screenToFlowPosition, getIntersectingNodes, getNodes } = useReactFlow();
  const { resolved } = useTheme();

  useEffect(() => { groupsRef.current = groups; }, [groups]);
  useEffect(() => { rfNodesRef.current = rfNodes; }, [rfNodes]);

  useEffect(() => {
    try { if (localStorage.getItem(HINT_KEY) === "off") setHintOpen(false); } catch { /* private mode */ }
  }, []);

  // saved positions from the DB — the source of truth for reloads
  const layout = useRef<PosMap>({});
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // A bus forming or splitting is animated instead of snapped. `pairState`
  // remembers each location pair's last logical shape ("one" one-way link vs
  // "bus" mutual) so the moment it changes can be caught. `busTx` holds the
  // in-flight transitions the reconcile below reads to render the fold/unfold,
  // and — for a split — to keep the bus on-screen until the collapse finishes.
  const pairState = useRef<Map<string, "one" | "bus">>(new Map());
  const pairInit = useRef(false);
  const busTx = useRef<Map<string, { kind: "form" | "split"; start: number }>>(new Map());
  const txTimers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
  // Bumped when a transition ends, so the reconcile re-runs and lands on the
  // final shape (bus for a form, the surviving one-way edge for a split).
  const [animTick, setAnimTick] = useState(0);
  useEffect(() => () => { for (const t of txTimers.current.values()) clearTimeout(t); }, []);

  // "devgroup", not "group": React Flow styles the built-in "group" type name,
  // which paints a stray box behind any custom component that borrows it.
  const nodeTypes = useMemo(
    () => ({ client: ClientNodeView, server: ServerNodeView, devgroup: GroupNode }),
    []
  );
  const edgeTypes = useMemo(() => ({ bus: BusEdge }), []);
  const servers = useMemo(() => vpnNodes.filter((n) => n.status === "active"), [vpnNodes]);

  // Load saved layout once before laying anything out.
  useEffect(() => {
    api.getLayout()
      .then((raw) => {
        const parsed = parseLayout(raw);
        layout.current = parsed.positions;
        setGroups(parsed.groups);
      })
      .catch(() => {})
      .finally(() => setReady(true));
  }, []);

  // One writer for the whole blob, so saving positions never drops groups and
  // vice versa.
  const saveLayout = useCallback((next: Partial<Layout>) => {
    if (next.positions) layout.current = next.positions;
    const groupsNow = next.groups ?? groupsRef.current;
    if (next.groups) {
      groupsRef.current = next.groups;
      setGroups(next.groups);
    }
    api.setLayout(serializeLayout({ positions: layout.current, groups: groupsNow })).catch(() => {});
  }, []);

  // Begin a fold (form) or collapse (split) for a location pair, and schedule
  // its end. When it ends the transition is dropped and a reconcile is forced,
  // so the edge settles onto its final shape.
  const startBusTx = useCallback((busId: string, kind: "form" | "split") => {
    if (busTx.current.has(busId)) return;
    if (typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    busTx.current.set(busId, { kind, start: performance.now() });
    const dur = kind === "form" ? FORM_MS : SPLIT_MS;
    const prev = txTimers.current.get(busId);
    if (prev) clearTimeout(prev);
    txTimers.current.set(busId, setTimeout(() => {
      busTx.current.delete(busId);
      txTimers.current.delete(busId);
      setAnimTick((t) => t + 1);
    }, dur + 40));
  }, []);

  // Reconcile the graph from data WITHOUT resetting positions: keep the position
  // a node already has this session (prev), else the DB layout, else a default.
  useEffect(() => {
    if (!ready) return;

    const colorOf = new Map<string, string>();
    for (const c of clients) colorOf.set(cid(c.id), nodeColor(c, resolved));
    for (const n of servers) colorOf.set(nid(n.id), nodeColor(n, resolved));

    // Groups only make sense for devices that still exist; a deleted device must
    // not leave a phantom member behind.
    const liveGroups = groups.map((g) => ({
      ...g,
      members: g.members.filter((m) => clients.some((c) => c.id === m)),
    }));
    // The frame, its badge, its ring and its outgoing lines all read from this
    // one value, so a group's chosen colour actually shows up on its edges.
    // markColor's mid band stays legible on both the light and dark canvas —
    // the card bands are tuned for fills, and would be near-invisible as a line.
    const groupAccent = (g: Group) =>
      g.color ? markColor({ color: g.color, address: "", id: g.id }) : GROUP_COLOR[resolved];
    for (const g of liveGroups) colorOf.set(gid(g.id), groupAccent(g));
    const inGroup = groupedClientIds(liveGroups);
    const grantsByClient = new Map(clients.map((c) => [c.id, c.granted_nodes]));
    const loose = clients.filter((c) => !inGroup.has(c.id));

    // Relations first: which cards to fade depends on what the selection touches.
    type Rel = { id: string; source: string; target: string; link: boolean; bus?: boolean; tx?: { kind: "form" | "split"; start: number } };
    const rels: Rel[] = [];
    for (const c of loose) {
      for (const g of c.granted_nodes) {
        if (servers.some((s) => s.id === g)) {
          rels.push({ id: `${c.id}=>${g}`, source: cid(c.id), target: nid(g), link: false });
        }
      }
    }
    // One line per destination for the whole group — the reason groups exist.
    for (const g of liveGroups) {
      for (const nodeId of groupGrants(g, grantsByClient)) {
        if (servers.some((s) => s.id === nodeId)) {
          rels.push({ id: `${gid(g.id)}=>${nodeId}`, source: gid(g.id), target: nid(nodeId), link: false });
        }
      }
    }
    const alive = (id: string) => servers.some((s) => s.id === id);
    const both = new Set(links.map((l) => `${l.src}>${l.dst}`));

    // The current logical shape of each live location pair, so a change from
    // one-way to mutual (or back) can be spotted and animated.
    const pairNow = new Map<string, "one" | "bus">();
    for (const l of links) {
      if (!alive(l.src) || !alive(l.dst)) continue;
      pairNow.set(busEdgeId(l.src, l.dst), both.has(`${l.dst}>${l.src}`) ? "bus" : "one");
    }
    if (!pairInit.current) {
      // First reconcile: adopt whatever exists without animating it — buses
      // already on the board when the page loads must not all fold in.
      pairState.current = pairNow;
      pairInit.current = true;
    } else {
      for (const [busId, shape] of pairNow) {
        const was = pairState.current.get(busId);
        if (was === "one" && shape === "bus") startBusTx(busId, "form");
      }
      for (const [busId, was] of pairState.current) {
        // Gone to a single direction: split. Gone entirely (both removed): let
        // it disappear — deleting a bus is a decisive act, not a fold.
        if (was === "bus" && pairNow.get(busId) === "one") startBusTx(busId, "split");
      }
      pairState.current = pairNow;
    }

    const drawn = new Set<string>();
    for (const l of links) {
      if (!alive(l.src) || !alive(l.dst)) continue;
      const busId = busEdgeId(l.src, l.dst);
      const tx = busTx.current.get(busId);
      if (both.has(`${l.dst}>${l.src}`)) {
        // Mutual: collapse the pair into a single bus, once.
        if (drawn.has(busId)) continue;
        drawn.add(busId);
        rels.push({ id: busId, source: nid(l.src), target: nid(l.dst), link: true, bus: true, tx });
        continue;
      }
      if (tx?.kind === "split") {
        // One direction left, but a collapse is still playing: hold the bus on
        // screen (and suppress the one-way edge) until it finishes.
        if (drawn.has(busId)) continue;
        drawn.add(busId);
        rels.push({ id: busId, source: nid(l.src), target: nid(l.dst), link: true, bus: true, tx });
        continue;
      }
      rels.push({ id: linkEdgeId(l.src, l.dst), source: nid(l.src), target: nid(l.dst), link: true });
    }

    // A selection that no longer exists (device deleted while selected) must not
    // fade the whole graph into an unreadable state.
    const sel = selectedId && colorOf.has(selectedId) ? selectedId : null;
    // A member has no lines of its own — the group carries them. Anchor the
    // highlight on the group so clicking a card inside one still shows what it
    // reaches instead of blanking the graph.
    const holder = sel?.startsWith("c:")
      ? liveGroups.find((g) => g.members.includes(unwrap(sel)))
      : undefined;
    const anchor = holder ? gid(holder.id) : sel;

    const connected = new Set<string>();
    if (anchor) {
      connected.add(anchor);
      if (sel) connected.add(sel);
      // Siblings stay lit: they share the group's access by definition.
      if (holder) for (const m of holder.members) connected.add(cid(m));
      for (const r of rels) {
        if (r.source === anchor) connected.add(r.target);
        if (r.target === anchor) connected.add(r.source);
      }
    }

    // While a connection is in flight, show where it may legally land: clients
    // reach any location (the internet exit included), locations link only to
    // other real locations.
    const canDrop = (rfId: string) => {
      if (!connectingFrom || rfId === connectingFrom) return false;
      if (connectingFrom.startsWith("c:") || isGroupId(connectingFrom)) return rfId.startsWith("n:");
      if (!rfId.startsWith("n:")) return false;
      const target = servers.find((x) => x.id === unwrap(rfId));
      return !!target && !target.is_hub;
    };

    let cn = 0, sn = 0;
    const prevNodes = rfNodesRef.current;
    const prevPos = new Map(prevNodes.map((n) => [n.id, n.position]));
    const pos = (id: string, def: XYPosition) => prevPos.get(id) ?? layout.current[id] ?? def;
    const next: RFNode[] = [
      // React Flow requires a parent to appear before its children.
      ...liveGroups.map((g) => {
        const id = gid(g.id);
        return {
          id, type: "devgroup",
          position: pos(id, { x: 20, y: 24 + cn++ * 96 }),
          style: { width: g.w ?? GROUP_DEF_W, height: g.h ?? GROUP_DEF_H },
          zIndex: connectingFrom ? 20 : id === sel ? 10 : 0,
          data: {
            label: g.name,
            count: g.members.length,
            color: groupAccent(g),
            sel: id === sel, dim: anchor !== null && !connected.has(id),
            candidate: canDrop(id), dragging: id === connectingFrom,
            muted: connectingFrom !== null && id !== connectingFrom && !canDrop(id),
            onGear: () => setEditingGroup(g.id),
            onResize: (w: number, h: number) => updateGroupRef.current?.(g.id, { w, h }),
          },
        } as RFNode;
      }),
      ...clients.map((c) => {
        const id = cid(c.id);
        const color = nodeColor(c, resolved);
        const holder = liveGroups.find((g) => g.members.includes(c.id));
        // Members sit inside their group; the group owns the line out, so a
        // member card carries no handle of its own.
        const inside = !!holder;
        let seat = pos(id, { x: 20, y: 24 + cn++ * 96 });
        if (inside) seat = layout.current[id] ?? memberSeat(holder!.members.indexOf(c.id));
        return {
          id, type: "client",
          position: seat,
          ...(inside ? { parentId: gid(holder!.id) } : {}),
          zIndex: connectingFrom ? 20 : id === sel ? 10 : 1,
          data: {
            label: c.name, sub: c.address, online: c.online, inside,
            sel: id === sel, dim: anchor !== null && !connected.has(id),
            candidate: canDrop(id), dragging: id === connectingFrom,
            muted: connectingFrom !== null && id !== connectingFrom && !canDrop(id),
            color, textColor: readableTextColor(color), accent: handleColor(c, resolved),
            onGear: () => setDetailsClientId(c.id),
          },
        } as RFNode;
      }),
      ...servers.map((n) => {
        const id = nid(n.id);
        const color = nodeColor(n, resolved);
        return {
          id, type: "server",
          position: pos(id, { x: 380, y: 24 + sn++ * 96 }),
          zIndex: connectingFrom ? 20 : id === sel ? 10 : 1,
          data: {
            label: n.is_hub ? "The internet" : n.name,
            sub: n.is_hub ? "all internet traffic" : n.subnets.join(", "),
            hub: n.is_hub, online: n.online,
            sel: id === sel, dim: anchor !== null && !connected.has(id),
            candidate: canDrop(id), dragging: id === connectingFrom,
            muted: connectingFrom !== null && id !== connectingFrom && !canDrop(id),
            color, textColor: readableTextColor(color), accent: handleColor(n, resolved),
            onGear: n.is_hub ? undefined : () => setRenamingNode({ id: n.id, name: n.name }),
          },
        } as RFNode;
      }),
    ];

    // Same reason as the edges below: handing React Flow a new nodes array on
    // every poll makes it rebuild DOM, and an edge rebuild restarts whatever
    // animates inside one. (stringify drops functions, so the per-render
    // callbacks don't register as a difference.)
    const nsig = JSON.stringify(next);
    if (nsig !== nodeSig.current) {
      nodeSig.current = nsig;
      setRfNodes(next);
    }

    const built = rels.map((r) => {
        // A bus is mutual, so it carries no arrowhead — there is no single
        // direction to point at — and is drawn heavy enough to read as a trunk.
        const width = r.bus ? BUS_WIDTH : 2;

        // Nothing selected: the resting look — quiet grants, orange site links.
        if (!anchor) {
          return r.link
            ? {
                id: r.id, source: r.source, target: r.target,
                ...(r.bus ? { type: "bus", data: { highlight: BUS_CORE[resolved], transition: r.tx } } : {}),
                style: { stroke: LINK_COLOR, strokeWidth: width },
                markerEnd: r.bus ? undefined : { type: MarkerType.ArrowClosed, color: LINK_COLOR },
              }
            : { id: r.id, source: r.source, target: r.target, animated: true };
        }

        // Selected: each line takes the color of the card at its far end, so a
        // device's reach is legible at a glance instead of one teal tangle. A
        // bus recolors by the same rule, only heavier.
        if (r.source === anchor || r.target === anchor) {
          const other = r.source === anchor ? r.target : r.source;
          const accent = colorOf.get(other) ?? LINK_COLOR;
          return {
            id: r.id, source: r.source, target: r.target,
            ...(r.bus ? { type: "bus", data: { highlight: BUS_CORE[resolved], transition: r.tx } } : {}),
            animated: !r.link,
            zIndex: 10,
            style: { stroke: accent, strokeWidth: r.bus ? BUS_WIDTH + 1 : 4 },
            markerEnd: r.link && !r.bus ? { type: MarkerType.ArrowClosed, color: accent } : undefined,
          };
        }

        // Not part of the selection: inert as well as faded. Editing a line you
        // pushed to the background is never what the click meant.
        return {
          id: r.id, source: r.source, target: r.target,
          ...(r.bus ? { type: "bus", data: { dim: true } } : {}),
          animated: false,
          zIndex: 0,
          selectable: false,
          deletable: false,
          focusable: false,
          interactionWidth: 0,
          style: { stroke: FADED[resolved], strokeWidth: r.bus ? 3 : 1.5, opacity: 0.35 },
          markerEnd: r.link && !r.bus ? { type: MarkerType.ArrowClosed, color: FADED[resolved] } : undefined,
        };
    });

    // The five-second poll hands back fresh arrays even when nothing changed,
    // and replacing the edges array makes React Flow rebuild the edge DOM —
    // which restarts anything animating inside an edge. Only push when the
    // edges genuinely differ.
    const sig = JSON.stringify(built);
    if (sig !== edgeSig.current) {
      edgeSig.current = sig;
      setEdges(built);
    }
  }, [clients, servers, links, groups, selectedId, connectingFrom, resolved, ready, animTick, startBusTx, setRfNodes, setEdges]);

  // Persist positions (debounced) when the user finishes dragging.
  const persist = useCallback((nodes: RFNode[]) => {
    const m: PosMap = {};
    for (const n of nodes) m[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
    layout.current = m;
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      api.setLayout(serializeLayout({ positions: layout.current, groups: groupsRef.current })).catch(() => {});
    }, 400);
  }, []);


  // --- groups -------------------------------------------------------------

  const createGroup = useCallback(() => {
    const g: Group = { id: newGroupId(), name: `Group ${groupsRef.current.length + 1}`, members: [] };
    if (dropAt.current) layout.current = { ...layout.current, [gid(g.id)]: dropAt.current };
    dropAt.current = null;
    saveLayout({ groups: [...groupsRef.current, g] });
    setEditingGroup(g.id);
  }, [saveLayout]);

  const updateGroup = useCallback((id: string, patch: Partial<Group>) => {
    saveLayout({ groups: groupsRef.current.map((g) => (g.id === id ? { ...g, ...patch } : g)) });
  }, [saveLayout]);

  const dissolveGroup = useCallback((id: string) => {
    saveLayout({ groups: groupsRef.current.filter((g) => g.id !== id) });
    setEditingGroup(null);
  }, [saveLayout]);

  useEffect(() => { updateGroupRef.current = updateGroup; }, [updateGroup]);

  // Bring a device's access in line with the group it landed in, then take it
  // in. No confirmation step — dropping a card is the decision. The access
  // change is reported by toast instead, because it can revoke as well as grant
  // and that must not pass unmentioned.
  const joinGroup = useCallback(async (group: Group, client: Client) => {
    const grantsByClient = new Map(clients.map((c) => [c.id, c.granted_nodes]));
    const target = group.members.length === 0 ? client.granted_nodes : groupGrants(group, grantsByClient);
    const { add, remove } = syncPlan(target, client.granted_nodes);

    updateGroup(group.id, { members: [...group.members, client.id] });
    try {
      for (const nodeId of add) await api.grant(client.id, nodeId);
      for (const nodeId of remove) await api.revoke(client.id, nodeId);
      const nameOf = (id: string) => {
        const n = servers.find((x) => x.id === id);
        return n ? (n.is_hub ? "the internet" : n.name) : "a location";
      };
      if (add.length) toast(`${client.name} now reaches ${add.map(nameOf).join(", ")}`, "warn");
      if (remove.length) toast(`${client.name} no longer reaches ${remove.map(nameOf).join(", ")}`, "warn");
      if (add.length || remove.length) configChanged(client.name);
      onChanged();
    } catch (e) {
      onError?.("Couldn't apply the group's access: " + humanError(e));
      onChanged();
    }
  }, [clients, servers, updateGroup, onChanged, onError]);

  // A card dropped on a group joins it; a member dragged clear of its group
  // leaves. Positions are converted between absolute and parent-relative here,
  // since React Flow stores child positions relative to the parent.
  const onClientDropped = useCallback((node: RFNode) => {
    const clientId = unwrap(node.id);
    const client = clients.find((c) => c.id === clientId);
    if (!client) return;
    const current = groupsRef.current.find((g) => g.members.includes(clientId));
    const hit = getIntersectingNodes(node).find((n) => isGroupId(n.id));

    if (hit && !current) {
      const group = groupsRef.current.find((g) => g.id === unwrap(hit.id));
      if (!group) return;
      // Dropped members line up in the stack rather than landing wherever the
      // pointer let go, which is what stops two cards sharing one spot.
      layout.current = { ...layout.current, [node.id]: memberSeat(group.members.length) };
      void joinGroup(group, client);
      return;
    }

    if (current && !hit) {
      // Dragged out: restore an absolute position so it doesn't jump.
      const origin = layout.current[gid(current.id)] ?? { x: 0, y: 0 };
      layout.current = {
        ...layout.current,
        [node.id]: {
          x: Math.round(node.position.x + origin.x),
          y: Math.round(node.position.y + origin.y),
        },
      };
      updateGroup(current.id, { members: current.members.filter((m) => m !== clientId) });
    }
  }, [clients, getIntersectingNodes, joinGroup, updateGroup]);

  const onConnect = useCallback(
    async (conn: Connection) => {
      // group -> node : the same grant, applied to every member.
      if (conn.source && isGroupId(conn.source) && conn.target?.startsWith("n:")) {
        const group = groupsRef.current.find((g) => g.id === unwrap(conn.source!));
        const nodeId = unwrap(conn.target);
        if (!group) return;
        try {
          for (const m of group.members) await api.grant(m, nodeId);
          if (group.members.length > 0) configChanged(`${group.name} (${group.members.length} devices)`);
          onChanged();
        } catch (e) { onError?.("Couldn't give access: " + humanError(e)); onChanged(); }
        return;
      }
      // client -> node : access grant.
      if (conn.source?.startsWith("c:") && conn.target?.startsWith("n:")) {
        const clientId = unwrap(conn.source);
        const nodeId = unwrap(conn.target);
        setEdges((eds) => addEdge({ ...conn, id: `${clientId}=>${nodeId}`, animated: true }, eds));
        try {
          await api.grant(clientId, nodeId);
          configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
          onChanged();
        } catch (e) { onError?.("Couldn't give access: " + humanError(e)); onChanged(); }
        return;
      }
      // node -> node : site-to-site link (directed).
      if (conn.source?.startsWith("n:") && conn.target?.startsWith("n:")) {
        const srcId = unwrap(conn.source);
        const dstId = unwrap(conn.target);
        if (srcId === dstId) { onError?.("A location can't link to itself."); return; }
        setEdges((eds) => addEdge({
          ...conn, id: linkEdgeId(srcId, dstId),
          style: { stroke: LINK_COLOR, strokeWidth: 2 },
          markerEnd: { type: MarkerType.ArrowClosed, color: LINK_COLOR },
        }, eds));
        try {
          await api.linkNodes(srcId, dstId);
          configChanged(servers.find((s) => s.id === srcId)?.name ?? "node");
          onChanged();
        } catch (e) {
          // surface validation errors (overlap / hub / inactive) and drop the edge
          onError?.("Couldn't link those locations: " + humanError(e));
          onChanged();
        }
        return;
      }
      onError?.("Drag from a device to a location to give access, or between two locations to link them.");
    },
    [setEdges, onChanged, onError, clients, servers]
  );

  // Belt and braces alongside the per-edge flags: whatever route a click or a
  // Delete press takes, a line outside the selection is not editable.
  // Mirrors the highlight rule: a member's lines belong to its group, so a
  // click on one of those lines is in-selection when the member is selected.
  const selectionAnchor = useMemo(() => {
    if (!selectedId) return null;
    if (selectedId.startsWith("c:")) {
      const g = groups.find((x) => x.members.includes(unwrap(selectedId)));
      if (g) return gid(g.id);
    }
    return selectedId;
  }, [selectedId, groups]);

  const inSelection = useCallback(
    (edge: { source?: string | null; target?: string | null }) =>
      !selectionAnchor || edge.source === selectionAnchor || edge.target === selectionAnchor,
    [selectionAnchor]
  );

  const onEdgesDelete = useCallback(
    async (removed: RFEdge[]) => {
      for (const e of removed) {
        if (!inSelection(e)) continue;
        if (e.source && isGroupId(e.source)) {
          const group = groupsRef.current.find((g) => g.id === unwrap(e.source));
          const nodeId = e.id.split("=>")[1];
          if (!group) continue;
          try {
            for (const m of group.members) await api.revoke(m, nodeId);
            configChanged(`${group.name} (${group.members.length} devices)`);
          } catch (err) { onError?.("Couldn't take away access: " + humanError(err)); }
          continue;
        }
        if (isBusEdge(e.id)) {
          // A bus stands for both directions, so removing it removes both.
          const [a, bId] = parseBusEdge(e.id);
          try {
            await api.unlinkNodes(a, bId);
            await api.unlinkNodes(bId, a);
            configChanged(servers.find((s) => s.id === a)?.name ?? "location");
          } catch (err) { onError?.("Couldn't remove the link: " + humanError(err)); }
          continue;
        }
        if (isLinkEdge(e.id)) {
          const [srcId, dstId] = parseLinkEdge(e.id);
          try {
            await api.unlinkNodes(srcId, dstId);
            configChanged(servers.find((s) => s.id === srcId)?.name ?? "node");
          } catch (err) { onError?.("Couldn't remove the link: " + humanError(err)); }
          continue;
        }
        const [clientId, nodeId] = e.id.split("=>");
        try {
          await api.revoke(clientId, nodeId);
          configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
        }
        catch (err) { onError?.("Couldn't take away access: " + humanError(err)); }
      }
      onChanged();
    },
    [onChanged, onError, clients, servers, inSelection]
  );

  const editServer = editing ? servers.find((s) => s.id === editing.nodeId) : null;
  const editClient = editing ? clients.find((c) => c.id === editing.clientId) : null;
  const detailsClient = detailsClientId ? clients.find((c) => c.id === detailsClientId) : null;

  const linkSrc = editingLink ? servers.find((s) => s.id === editingLink.src) : null;
  const linkDst = editingLink ? servers.find((s) => s.id === editingLink.dst) : null;
  const linkHasReverse = editingLink
    ? links.some((l) => l.src === editingLink.dst && l.dst === editingLink.src)
    : false;

  return (
    <>
      <ReactFlow
        nodes={rfNodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onConnectStart={(_, { nodeId, handleType }) => {
          if (handleType === "source" && nodeId) setConnectingFrom(nodeId);
        }}
        onConnectEnd={() => setConnectingFrom(null)}
        onEdgesDelete={onEdgesDelete}
        onNodeDragStop={(_, node) => {
          // Order matters and must be synchronous. A setRfNodes updater runs on
          // the NEXT render, so persisting inside one landed after the drop
          // handler and overwrote the parent-relative seat it had just computed
          // with the card's old absolute position — members then drew outside
          // their own frame. getNodes() reads the same state here and now.
          persist(getNodes());
          if (node.id.startsWith("c:")) onClientDropped(node);
        }}
        snapToGrid
        snapGrid={[8, 8]}
        onNodeClick={(_, node) => setSelectedId((cur) => (cur === node.id ? null : node.id))}
        onPaneClick={() => { setSelectedId(null); setMenu(null); }}
        onPaneContextMenu={(e) => {
          e.preventDefault();
          const ev = e as React.MouseEvent;
          dropAt.current = screenToFlowPosition({ x: ev.clientX, y: ev.clientY });
          setMenu({ x: ev.clientX, y: ev.clientY });
        }}
        onMoveStart={() => setMenu(null)}
        onEdgeClick={(_, edge) => {
          if (!inSelection(edge)) return;
          if (edge.source && isGroupId(edge.source)) {
            setEditingGroupRules({ groupId: unwrap(edge.source), nodeId: edge.id.split("=>")[1] });
            return;
          }
          if (isBusEdge(edge.id)) {
            const [src, dst] = parseBusEdge(edge.id);
            setEditingLink({ src, dst });
            return;
          }
          if (isLinkEdge(edge.id)) {
            const [src, dst] = parseLinkEdge(edge.id);
            setEditingLink({ src, dst });
            return;
          }
          const [clientId, nodeId] = edge.id.split("=>");
          setEditing({ clientId, nodeId });
        }}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        deleteKeyCode={["Delete", "Backspace"]}
        fitView
        colorMode={resolved}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color={GRID[resolved]} />
        <Controls showInteractive={false} />
      </ReactFlow>

      {menu && (
        <>
          {/* click-away catcher, so the menu closes on any stray click */}
          <div className="menu-catch" onClick={() => setMenu(null)} onContextMenu={(e) => { e.preventDefault(); setMenu(null); }} />
          <div className="ctxmenu" style={{ left: menu.x, top: menu.y }} role="menu">
            <button role="menuitem" onClick={() => { setMenu(null); setAddingDevice(true); }}>
              Add a device here
            </button>
            <button role="menuitem" onClick={() => { setMenu(null); createGroup(); }}>
              Create a group here
            </button>
          </div>
        </>
      )}

      {addingDevice && (
        <AddDeviceWizard
          locations={servers}
          onDone={(createdId) => {
            // Drop the new card where the right-click happened rather than at the
            // default corner, which is the point of adding it from the canvas.
            if (createdId && dropAt.current) {
              layout.current = { ...layout.current, [cid(createdId)]: dropAt.current };
              api.setLayout(layout.current).catch(() => {});
              dropAt.current = null;
            }
            onChanged();
          }}
          onClose={() => setAddingDevice(false)}
        />
      )}


      {editingGroupRules && (() => {
        const g = groups.find((x) => x.id === editingGroupRules.groupId);
        const n = servers.find((x) => x.id === editingGroupRules.nodeId);
        if (!g || !n || g.members.length === 0) return null;
        return (
          <RulesModal
            clientIds={g.members}
            nodeId={n.id}
            clientName={`${g.name} (${g.members.length} devices)`}
            nodeName={n.is_hub ? "the internet" : n.name}
            subnetHints={n.subnets}
            onRevoke={async () => {
              try {
                for (const m of g.members) await api.revoke(m, n.id);
                configChanged(`${g.name} (${g.members.length} devices)`);
              } catch (e) { onError?.("Couldn't take away access: " + humanError(e)); }
              setEditingGroupRules(null);
              onChanged();
            }}
            onClose={() => { setEditingGroupRules(null); onChanged(); }}
          />
        );
      })()}

      {editingGroup && (() => {
        const g = groups.find((x) => x.id === editingGroup);
        if (!g) return null;
        return (
          <GroupModal
            group={g}
            members={g.members.map((m) => clients.find((c) => c.id === m)).filter(Boolean) as Client[]}
            onRename={(name) => updateGroup(g.id, { name })}
            onColor={(color) => updateGroup(g.id, { color })}
            onRemoveMember={(clientId) =>
              updateGroup(g.id, { members: g.members.filter((m) => m !== clientId) })
            }
            onDissolve={() => dissolveGroup(g.id)}
            onClose={() => setEditingGroup(null)}
          />
        );
      })()}

      {hintOpen && (
        <div className="stage-hint">
          <span>
            Click a card to see just what it reaches — the gear on it opens its settings. Drag from
            a device to a location to give access, or between two locations to link their networks.
            Click a line to change or remove it.
          </span>
          <button
            className="stage-hint-x"
            aria-label="Hide this tip"
            onClick={() => {
              setHintOpen(false);
              try { localStorage.setItem(HINT_KEY, "off"); } catch { /* private mode */ }
            }}
          >
            ✕
          </button>
        </div>
      )}

      {editing && editServer && editClient && (
        <RulesModal
          clientIds={[editing.clientId]}
          nodeId={editing.nodeId}
          clientName={editClient.name}
          nodeName={editServer.name}
          subnetHints={editServer.subnets}
          onRevoke={async () => {
            try {
              await api.revoke(editing.clientId, editing.nodeId);
              configChanged(editClient.name);
            } catch { /* surfaced by reload */ }
            setEditing(null);
            onChanged();
          }}
          onClose={() => { setEditing(null); onChanged(); }}
        />
      )}

      {editingLink && linkSrc && linkDst && (
        <LinkModal
          srcName={linkSrc.name}
          dstName={linkDst.name}
          hasReverse={linkHasReverse}
          onAddReverse={async () => {
            try { await api.linkNodes(editingLink.dst, editingLink.src); configChanged(linkDst.name); }
            catch (e) { onError?.("Couldn't add the other direction: " + humanError(e)); }
            onChanged();
          }}
          onRemoveReverse={async () => {
            try { await api.unlinkNodes(editingLink.dst, editingLink.src); configChanged(linkDst.name); }
            catch (e) { onError?.("Couldn't remove the other direction: " + humanError(e)); }
            onChanged();
          }}
          onRemove={async () => {
            try { await api.unlinkNodes(editingLink.src, editingLink.dst); configChanged(linkSrc.name); }
            catch (e) { onError?.("Couldn't remove the link: " + humanError(e)); }
            onChanged();
          }}
          onClose={() => { setEditingLink(null); onChanged(); }}
        />
      )}

      {detailsClient && (
        <ClientDetails
          client={detailsClient}
          onRename={async (name) => {
            try { await api.renameClient(detailsClient.id, name); }
            catch (e) { onError?.("Couldn't rename: " + humanError(e)); }
            onChanged();
          }}
          onColor={async (color) => {
            try { await api.setClientColor(detailsClient.id, color); }
            catch (e) { onError?.("Couldn't change the color: " + humanError(e)); }
            onChanged();
          }}
          onDelete={async () => {
            try { await api.deleteClient(detailsClient.id); }
            catch (e) { onError?.("Couldn't remove it: " + humanError(e)); }
            setDetailsClientId(null);
            onChanged();
          }}
          onClose={() => setDetailsClientId(null)}
        />
      )}

      {renamingNode && (
        <RenameModal
          title="Rename location"
          current={renamingNode.name}
          onSave={async (name) => {
            try { await api.renameNode(renamingNode.id, name); }
            catch (e) { onError?.("Couldn't rename: " + humanError(e)); }
            onChanged();
          }}
          onClose={() => setRenamingNode(null)}
        />
      )}

    </>
  );
}
