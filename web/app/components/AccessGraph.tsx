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
  type Node as RFNode,
  type Edge as RFEdge,
  type Connection,
  type NodeProps,
  type XYPosition,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api, Node as VpnNode, Client } from "../lib/api";
import RulesModal from "./RulesModal";
import ClientDetails from "./ClientDetails";
import RenameModal from "./RenameModal";
import { configChanged } from "../lib/toast";

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
    </div>
  );
}

export default function AccessGraph({
  nodes: vpnNodes,
  clients,
  onChanged,
  selectedClientId,
  onError,
}: {
  nodes: VpnNode[];
  clients: Client[];
  onChanged: () => void;
  selectedClientId?: string | null;
  onError?: (msg: string) => void;
}) {
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<RFNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [editing, setEditing] = useState<{ clientId: string; nodeId: string } | null>(null);
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
    setEdges(built_edges);
  }, [clients, servers, selectedClientId, ready, setRfNodes, setEdges]);

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
      if (!conn.source?.startsWith("c:") || !conn.target?.startsWith("n:")) {
        onError?.("Draw the arrow from a client to a node.");
        return;
      }
      const clientId = unwrap(conn.source);
      const nodeId = unwrap(conn.target);
      setEdges((eds) => addEdge({ ...conn, id: `${clientId}=>${nodeId}`, animated: true }, eds));
      try {
        await api.grant(clientId, nodeId);
        configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
        onChanged();
      }
      catch (e) { onError?.("Couldn't grant access: " + String(e)); onChanged(); }
    },
    [setEdges, onChanged, onError, clients]
  );

  const onEdgesDelete = useCallback(
    async (removed: RFEdge[]) => {
      for (const e of removed) {
        const [clientId, nodeId] = e.id.split("=>");
        try {
          await api.revoke(clientId, nodeId);
          configChanged(clients.find((c) => c.id === clientId)?.name ?? "client");
        }
        catch (err) { onError?.("Couldn't revoke access: " + String(err)); }
      }
      onChanged();
    },
    [onChanged, onError, clients]
  );

  const editServer = editing ? servers.find((s) => s.id === editing.nodeId) : null;
  const editClient = editing ? clients.find((c) => c.id === editing.clientId) : null;
  const detailsClient = detailsClientId ? clients.find((c) => c.id === detailsClientId) : null;

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
        drag client → node to grant · click an arrow to edit or revoke access
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
