"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import {
  ReactFlow,
  Background,
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
import { api, ApiError, Node as VpnNode, Client } from "../lib/api";
import RulesModal from "../components/RulesModal";

// Prefixed RF ids so we can tell clients from server-nodes on a connection.
const cid = (id: string) => `c:${id}`;
const nid = (id: string) => `n:${id}`;
const unwrap = (rfId: string) => rfId.slice(2);

function ClientNodeView({ data }: NodeProps) {
  const d = data as { label: string; sub: string };
  return (
    <div className="gnode gnode-client">
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
      <div className="gnode-title">{d.hub ? "🌐 " : ""}{d.label}</div>
      <div className="gnode-sub">{d.sub}</div>
    </div>
  );
}

export default function GraphPage() {
  const router = useRouter();
  const [nodes, setNodes, onNodesChange] = useNodesState<RFNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [err, setErr] = useState("");
  // raw data kept so an edge click can resolve names + subnet hints
  const [clients, setClients] = useState<Client[]>([]);
  const [servers, setServers] = useState<VpnNode[]>([]);
  const [editing, setEditing] = useState<{ clientId: string; nodeId: string } | null>(null);

  const nodeTypes = useMemo(
    () => ({ client: ClientNodeView, server: ServerNodeView }),
    []
  );

  const load = useCallback(async () => {
    try {
      const [ns, cs] = await Promise.all([api.listNodes(), api.listClients()]);
      const servers = (ns || []).filter((n) => n.status === "active");
      const clients = cs || [];
      setServers(servers);
      setClients(clients);

      const rfNodes: RFNode[] = [
        ...clients.map((c: Client, i: number) => ({
          id: cid(c.id),
          type: "client",
          position: { x: 40, y: 40 + i * 110 },
          data: { label: c.name, sub: c.address },
        })),
        ...servers.map((n: VpnNode, i: number) => ({
          id: nid(n.id),
          type: "server",
          position: { x: 520, y: 40 + i * 110 },
          data: {
            label: n.is_hub ? "internet exit" : n.name,
            sub: n.is_hub ? "via panel (0.0.0.0/0)" : n.subnets.join(", "),
            hub: n.is_hub,
          },
        })),
      ];

      const rfEdges: RFEdge[] = [];
      for (const c of clients) {
        for (const g of c.granted_nodes) {
          if (servers.some((s) => s.id === g)) {
            rfEdges.push({ id: `${c.id}=>${g}`, source: cid(c.id), target: nid(g), animated: true });
          }
        }
      }
      setNodes(rfNodes);
      setEdges(rfEdges);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) router.push("/login");
      else setErr(String(e));
    }
  }, [router, setNodes, setEdges]);

  useEffect(() => {
    load();
  }, [load]);

  // Draw an arrow client -> server = grant.
  const onConnect = useCallback(
    async (conn: Connection) => {
      if (!conn.source?.startsWith("c:") || !conn.target?.startsWith("n:")) {
        setErr("connect from a client to a node");
        return;
      }
      const clientId = unwrap(conn.source);
      const nodeId = unwrap(conn.target);
      setEdges((eds) =>
        addEdge({ ...conn, id: `${clientId}=>${nodeId}`, animated: true }, eds)
      );
      try {
        await api.grant(clientId, nodeId);
        setErr("");
      } catch (e) {
        setErr("grant failed: " + String(e));
        load();
      }
    },
    [setEdges, load]
  );

  // Removing an arrow = revoke.
  const onEdgesDelete = useCallback(
    async (removed: RFEdge[]) => {
      for (const e of removed) {
        const [clientId, nodeId] = e.id.split("=>");
        try {
          await api.revoke(clientId, nodeId);
        } catch (err) {
          setErr("revoke failed: " + String(err));
          load();
        }
      }
    },
    [load]
  );

  const editServer = editing ? servers.find((s) => s.id === editing.nodeId) : null;
  const editClient = editing ? clients.find((c) => c.id === editing.clientId) : null;

  return (
    <>
      <header className="topbar">
        <div className="brand">
          beautiful<span style={{ color: "var(--accent)" }}>wg</span> · access graph
        </div>
        <div style={{ display: "flex", gap: 10 }}>
          <Link href="/"><button className="ghost">Table view</button></Link>
        </div>
      </header>

      <div className="mono" style={{ padding: "6px 16px", color: "var(--muted)" }}>
        Drag client → node to grant · click an arrow to set its access level (subnets + ports) ·
        select an arrow and press Delete to revoke
      </div>
      {err && <div className="error" style={{ padding: "8px 16px" }}>{err}</div>}

      <div style={{ height: "calc(100vh - 84px)" }}>
        <ReactFlow
          nodes={nodes}
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
        >
          <Background />
          <Controls />
        </ReactFlow>
      </div>

      {editing && editServer && editClient && (
        <RulesModal
          clientId={editing.clientId}
          nodeId={editing.nodeId}
          clientName={editClient.name}
          nodeName={editServer.name}
          subnetHints={editServer.subnets}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}
