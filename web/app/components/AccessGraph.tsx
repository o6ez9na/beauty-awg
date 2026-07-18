"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ReactFlow,
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
import ClientDetails from "./ClientDetails";
import RenameModal from "./RenameModal";
import LinkModal from "./LinkModal";
import { configChanged } from "../lib/toast";

// site-to-site (node->node) edge accent, distinct from the blue grant edges.
const LINK_COLOR = "#f0a020";
const linkEdgeId = (src: string, dst: string) => `lnk:${src}~${dst}`;
const isLinkEdge = (id: string) => id.startsWith("lnk:");
const parseLinkEdge = (id: string) => id.slice(4).split("~"); // [src, dst]

const cid = (id: string) => `c:${id}`;
const nid = (id: string) => `n:${id}`;
const unwrap = (rfId: string) => rfId.slice(2);

type PosMap = Record<string, XYPosition>;

function Dot({ online }: { online?: boolean }) {
  return <span className={"dot " + (online ? "live" : "")} style={{ marginRight: 7 }} />;
}

function ClientNodeView({ data }: NodeProps) {
  const d = data as { label: string; sub: string; sel?: boolean; online?: boolean };
  return (
    <div className={"gnode gnode-client" + (d.sel ? " sel" : "")}>
      <div className="gnode-title"><Dot online={d.online} />{d.label}</div>
      <div className="gnode-sub">{d.sub}</div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

function ServerNodeView({ data }: NodeProps) {
  const d = data as { label: string; sub: string; hub?: boolean; online?: boolean };
  return (
    <div className={"gnode " + (d.hub ? "gnode-hub" : "gnode-server")}>
      <Handle type="target" position={Position.Left} />
      <div className="gnode-title">{d.hub ? "◍ " : <Dot online={d.online} />}{d.label}</div>
      <div className="gnode-sub">{d.sub}</div>
      {/* real nodes can also be a link SOURCE (drag from the right to another node) */}
      {!d.hub && <Handle type="source" position={Position.Right} />}
    </div>
  );
}

export default function AccessGraph({
  nodes: vpnNodes,
  clients,
  links,
  onChanged,
  selectedClientId,
  onError,
}: {
  nodes: VpnNode[];
  clients: Client[];
  links: NodeLink[];
  onChanged: () => void;
  selectedClientId?: string | null;
  onError?: (msg: string) => void;
}) {
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<RFNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [editing, setEditing] = useState<{ clientId: string; nodeId: string } | null>(null);
  const [editingLink, setEditingLink] = useState<{ src: string; dst: string } | null>(null);
  const [detailsClientId, setDetailsClientId] = useState<string | null>(null);
  const [renamingNode, setRenamingNode] = useState<{ id: string; name: string } | null>(null);
  const [ready, setReady] = useState(false);

  // saved positions from the DB — the source of truth for reloads
  const layout = useRef<PosMap>({});
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const nodeTypes = useMemo(() => ({ client: ClientNodeView, server: ServerNodeView }), []);
  const servers = useMemo(() => vpnNodes.filter((n) => n.status === "active"), [vpnNodes]);

  // Load saved layout once before laying anything out.
  useEffect(() => {
    api.getLayout().then((m) => { layout.current = m || {}; }).catch(() => {}).finally(() => setReady(true));
  }, []);

  // Reconcile the graph from data WITHOUT resetting positions: keep the position
  // a node already has this session (prev), else the DB layout, else a default.
  useEffect(() => {
    if (!ready) return;
    let cn = 0, sn = 0;
    setRfNodes((prev) => {
      const prevPos = new Map(prev.map((n) => [n.id, n.position]));
      const pos = (id: string, def: XYPosition) => prevPos.get(id) ?? layout.current[id] ?? def;
      return [
        ...clients.map((c) => {
          const id = cid(c.id);
          return {
            id, type: "client",
            position: pos(id, { x: 20, y: 24 + cn++ * 96 }),
            data: { label: c.name, sub: c.address, sel: c.id === selectedClientId, online: c.online },
          } as RFNode;
        }),
        ...servers.map((n) => {
          const id = nid(n.id);
          return {
            id, type: "server",
            position: pos(id, { x: 380, y: 24 + sn++ * 96 }),
            data: {
              label: n.is_hub ? "internet exit" : n.name,
              sub: n.is_hub ? "via panel · 0.0.0.0/0" : n.subnets.join(", "),
              hub: n.is_hub, online: n.online,
            },
          } as RFNode;
        }),
      ];
    });

    const built_edges: RFEdge[] = [];
    for (const c of clients) {
      for (const g of c.granted_nodes) {
        if (servers.some((s) => s.id === g)) {
          built_edges.push({ id: `${c.id}=>${g}`, source: cid(c.id), target: nid(g), animated: true });
        }
      }
    }
    // Site-to-site node->node links, drawn in a distinct color with an arrowhead.
    for (const l of links) {
      if (servers.some((s) => s.id === l.src) && servers.some((s) => s.id === l.dst)) {
        built_edges.push({
          id: linkEdgeId(l.src, l.dst),
          source: nid(l.src),
          target: nid(l.dst),
          style: { stroke: LINK_COLOR, strokeWidth: 2 },
          markerEnd: { type: MarkerType.ArrowClosed, color: LINK_COLOR },
        });
      }
    }
    setEdges(built_edges);
  }, [clients, servers, links, selectedClientId, ready, setRfNodes, setEdges]);

  // Persist positions (debounced) when the user finishes dragging.
  const persist = useCallback((nodes: RFNode[]) => {
    const m: PosMap = {};
    for (const n of nodes) m[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
    layout.current = m;
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => { api.setLayout(m).catch(() => {}); }, 400);
  }, []);

  const onConnect = useCallback(
    async (conn: Connection) => {
      // client -> node : access grant.
      if (conn.source?.startsWith("c:") && conn.target?.startsWith("n:")) {
        const clientId = unwrap(conn.source);
        const nodeId = unwrap(conn.target);
        setEdges((eds) => addEdge({ ...conn, id: `${clientId}=>${nodeId}`, animated: true }, eds));
        try {
          await api.grant(clientId, nodeId);
          configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
          onChanged();
        } catch (e) { onError?.("Couldn't grant access: " + String(e)); onChanged(); }
        return;
      }
      // node -> node : site-to-site link (directed).
      if (conn.source?.startsWith("n:") && conn.target?.startsWith("n:")) {
        const srcId = unwrap(conn.source);
        const dstId = unwrap(conn.target);
        if (srcId === dstId) { onError?.("A node cannot link to itself."); return; }
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
          onError?.("Couldn't link nodes: " + (e instanceof Error ? e.message : String(e)));
          onChanged();
        }
        return;
      }
      onError?.("Draw an arrow from a client to a node, or from one node to another.");
    },
    [setEdges, onChanged, onError, clients, servers]
  );

  const onEdgesDelete = useCallback(
    async (removed: RFEdge[]) => {
      for (const e of removed) {
        if (isLinkEdge(e.id)) {
          const [srcId, dstId] = parseLinkEdge(e.id);
          try {
            await api.unlinkNodes(srcId, dstId);
            configChanged(servers.find((s) => s.id === srcId)?.name ?? "node");
          } catch (err) { onError?.("Couldn't remove link: " + String(err)); }
          continue;
        }
        const [clientId, nodeId] = e.id.split("=>");
        try {
          await api.revoke(clientId, nodeId);
          configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
        }
        catch (err) { onError?.("Couldn't revoke access: " + String(err)); }
      }
      onChanged();
    },
    [onChanged, onError, clients, servers]
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
        onEdgesDelete={onEdgesDelete}
        onNodeDragStop={() => setRfNodes((prev) => { persist(prev); return prev; })}
        onNodeClick={(_, node) => {
          if (node.id.startsWith("c:")) {
            setDetailsClientId(unwrap(node.id));
          } else if (node.id.startsWith("n:")) {
            const s = servers.find((x) => x.id === unwrap(node.id));
            if (s && !s.is_hub) setRenamingNode({ id: s.id, name: s.name });
          }
        }}
        onEdgeClick={(_, edge) => {
          if (isLinkEdge(edge.id)) {
            const [src, dst] = parseLinkEdge(edge.id);
            setEditingLink({ src, dst });
            return;
          }
          const [clientId, nodeId] = edge.id.split("=>");
          setEditing({ clientId, nodeId });
        }}
        nodeTypes={nodeTypes}
        deleteKeyCode={["Delete", "Backspace"]}
        fitView
        colorMode="dark"
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#1b2430" />
        <Controls showInteractive={false} />
      </ReactFlow>

      <div className="stage-hint">
        drag client → node to grant · node → node for a site-to-site link · click an arrow to edit
      </div>

      {editing && editServer && editClient && (
        <RulesModal
          clientId={editing.clientId}
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
            catch (e) { onError?.("Couldn't add reverse: " + (e instanceof Error ? e.message : String(e))); }
            onChanged();
          }}
          onRemoveReverse={async () => {
            try { await api.unlinkNodes(editingLink.dst, editingLink.src); configChanged(linkDst.name); }
            catch (e) { onError?.("Couldn't remove reverse: " + String(e)); }
            onChanged();
          }}
          onRemove={async () => {
            try { await api.unlinkNodes(editingLink.src, editingLink.dst); configChanged(linkSrc.name); }
            catch (e) { onError?.("Couldn't remove link: " + String(e)); }
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
            catch (e) { onError?.("Couldn't rename: " + String(e)); }
            onChanged();
          }}
          onDelete={async () => {
            try { await api.deleteClient(detailsClient.id); }
            catch (e) { onError?.("Couldn't delete: " + String(e)); }
            setDetailsClientId(null);
            onChanged();
          }}
          onClose={() => setDetailsClientId(null)}
        />
      )}

      {renamingNode && (
        <RenameModal
          title="Rename node"
          current={renamingNode.name}
          onSave={async (name) => {
            try { await api.renameNode(renamingNode.id, name); }
            catch (e) { onError?.("Couldn't rename: " + String(e)); }
            onChanged();
          }}
          onClose={() => setRenamingNode(null)}
        />
      )}
    </>
  );
}
