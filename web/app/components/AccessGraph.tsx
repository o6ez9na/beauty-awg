"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
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
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api, Node as VpnNode, Client } from "../lib/api";
import RulesModal from "./RulesModal";

const cid = (id: string) => `c:${id}`;
const nid = (id: string) => `n:${id}`;
const unwrap = (rfId: string) => rfId.slice(2);

function ClientNodeView({ data }: NodeProps) {
  const d = data as { label: string; sub: string; sel?: boolean };
  return (
    <div className={"gnode gnode-client" + (d.sel ? " sel" : "")}>
      <div className="gnode-title">{d.label}</div>
      <div className="gnode-sub">{d.sub}</div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

function ServerNodeView({ data }: NodeProps) {
  const d = data as { label: string; sub: string; hub?: boolean };
  return (
    <div className={"gnode " + (d.hub ? "gnode-hub" : "gnode-server")}>
      <Handle type="target" position={Position.Left} />
      <div className="gnode-title">{d.hub ? "◍ " : ""}{d.label}</div>
      <div className="gnode-sub">{d.sub}</div>
    </div>
  );
}

// AccessGraph draws clients (left) → nodes / internet-exit (right). Drag an
// arrow to grant, select + Delete to revoke, click an arrow to set its level.
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

  const nodeTypes = useMemo(() => ({ client: ClientNodeView, server: ServerNodeView }), []);
  const servers = useMemo(() => vpnNodes.filter((n) => n.status === "active"), [vpnNodes]);

  useEffect(() => {
    const built: RFNode[] = [
      ...clients.map((c, i) => ({
        id: cid(c.id),
        type: "client",
        position: { x: 20, y: 24 + i * 96 },
        data: { label: c.name, sub: c.address, sel: c.id === selectedClientId },
      })),
      ...servers.map((n, i) => ({
        id: nid(n.id),
        type: "server",
        position: { x: 380, y: 24 + i * 96 },
        data: {
          label: n.is_hub ? "internet exit" : n.name,
          sub: n.is_hub ? "via panel · 0.0.0.0/0" : n.subnets.join(", "),
          hub: n.is_hub,
        },
      })),
    ];
    const built_edges: RFEdge[] = [];
    for (const c of clients) {
      for (const g of c.granted_nodes) {
        if (servers.some((s) => s.id === g)) {
          built_edges.push({
            id: `${c.id}=>${g}`,
            source: cid(c.id),
            target: nid(g),
            animated: true,
          });
        }
      }
    }
    setRfNodes(built);
    setEdges(built_edges);
  }, [clients, servers, selectedClientId, setRfNodes, setEdges]);

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
        onChanged();
      } catch (e) {
        onError?.("Couldn't grant access: " + String(e));
        onChanged();
      }
    },
    [setEdges, onChanged, onError]
  );

  const onEdgesDelete = useCallback(
    async (removed: RFEdge[]) => {
      for (const e of removed) {
        const [clientId, nodeId] = e.id.split("=>");
        try {
          await api.revoke(clientId, nodeId);
        } catch (err) {
          onError?.("Couldn't revoke access: " + String(err));
        }
      }
      onChanged();
    },
    [onChanged, onError]
  );

  const editServer = editing ? servers.find((s) => s.id === editing.nodeId) : null;
  const editClient = editing ? clients.find((c) => c.id === editing.clientId) : null;

  return (
    <>
      <ReactFlow
        nodes={rfNodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onEdgesDelete={onEdgesDelete}
        onEdgeClick={(_, edge) => {
          const [clientId, nodeId] = edge.id.split("=>");
          setEditing({ clientId, nodeId });
        }}
        nodeTypes={nodeTypes}
        fitView
        colorMode="dark"
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#1b2430" />
        <Controls showInteractive={false} />
      </ReactFlow>

      <div className="stage-hint">
        drag client → node to grant · click an arrow for access level · select + Delete to revoke
      </div>

      {editing && editServer && editClient && (
        <RulesModal
          clientId={editing.clientId}
          nodeId={editing.nodeId}
          clientName={editClient.name}
          nodeName={editServer.name}
          subnetHints={editServer.subnets}
          onClose={() => {
            setEditing(null);
            onChanged();
          }}
        />
      )}
    </>
  );
}
