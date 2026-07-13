"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, Node, Client } from "./lib/api";
import ConfigModal from "./components/ConfigModal";

export default function Dashboard() {
  const router = useRouter();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [clients, setClients] = useState<Client[]>([]);
  const [err, setErr] = useState("");
  const [modal, setModal] = useState<{ title: string; url: string; filename: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [n, c] = await Promise.all([api.listNodes(), api.listClients()]);
      setNodes(n || []);
      setClients(c || []);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        router.push("/login");
        return;
      }
      setErr(String(e));
    }
  }, [router]);

  useEffect(() => {
    load();
  }, [load]);

  async function guard(fn: () => Promise<void>) {
    setErr("");
    try {
      await fn();
      await load();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    }
  }

  return (
    <>
      <header className="topbar">
        <div className="brand">
          beautiful<span style={{ color: "var(--accent)" }}>wg</span>
        </div>
        <button
          className="ghost"
          onClick={() => api.logout().then(() => router.push("/login"))}
        >
          Logout
        </button>
      </header>

      <div className="container">
        {err && <div className="error">{err}</div>}

        <NodesCard nodes={nodes} onChange={guard} onConfig={setModal} />
        <ClientsCard
          clients={clients}
          nodes={nodes}
          onChange={guard}
          onConfig={setModal}
        />
      </div>

      {modal && (
        <ConfigModal
          title={modal.title}
          url={modal.url}
          filename={modal.filename}
          onClose={() => setModal(null)}
        />
      )}
    </>
  );
}

// ---------------- Nodes ----------------

function NodesCard({
  nodes,
  onChange,
  onConfig,
}: {
  nodes: Node[];
  onChange: (fn: () => Promise<void>) => void;
  onConfig: (m: { title: string; url: string; filename: string }) => void;
}) {
  const [name, setName] = useState("");
  const [iface, setIface] = useState("eth0");
  const [subnets, setSubnets] = useState("");

  function add() {
    const list = subnets
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onChange(async () => {
      await api.createNode(name, iface, list);
      setName("");
      setSubnets("");
    });
  }

  return (
    <div className="card">
      <h2>Nodes (home servers)</h2>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Tunnel IP</th>
            <th>LAN subnets</th>
            <th>LAN iface</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => (
            <tr key={n.id}>
              <td>{n.name}</td>
              <td className="mono">{n.address}</td>
              <td className="mono">{n.subnets.join(", ")}</td>
              <td className="mono">{n.lan_iface}</td>
              <td className="actions">
                <button
                  className="ghost"
                  onClick={() =>
                    onConfig({
                      title: `Node: ${n.name}`,
                      url: api.nodeConfigUrl(n.id),
                      filename: `${n.name}.conf`,
                    })
                  }
                >
                  Config
                </button>
                <button className="danger" onClick={() => onChange(() => api.deleteNode(n.id))}>
                  Delete
                </button>
              </td>
            </tr>
          ))}
          {nodes.length === 0 && (
            <tr>
              <td colSpan={5} className="mono">
                no nodes yet
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <div className="row" style={{ marginTop: 16 }}>
        <div className="field" style={{ flex: 1 }}>
          <label>Name</label>
          <input type="text" value={name} onChange={(e) => setName(e.target.value)} placeholder="home1" />
        </div>
        <div className="field" style={{ flex: 2 }}>
          <label>LAN subnets (comma-separated)</label>
          <input
            type="text"
            value={subnets}
            onChange={(e) => setSubnets(e.target.value)}
            placeholder="192.168.1.0/24, 10.31.31.0/24"
          />
        </div>
        <div className="field" style={{ width: 120 }}>
          <label>LAN iface</label>
          <input type="text" value={iface} onChange={(e) => setIface(e.target.value)} />
        </div>
        <button onClick={add} disabled={!name || !subnets}>
          Add node
        </button>
      </div>
    </div>
  );
}

// ---------------- Clients ----------------

function ClientsCard({
  clients,
  nodes,
  onChange,
  onConfig,
}: {
  clients: Client[];
  nodes: Node[];
  onChange: (fn: () => Promise<void>) => void;
  onConfig: (m: { title: string; url: string; filename: string }) => void;
}) {
  const [name, setName] = useState("");
  const [dns, setDns] = useState("");

  function add() {
    onChange(async () => {
      await api.createClient(name, dns);
      setName("");
      setDns("");
    });
  }

  function toggleGrant(c: Client, nodeId: string) {
    const has = c.granted_nodes.includes(nodeId);
    onChange(() => (has ? api.revoke(c.id, nodeId) : api.grant(c.id, nodeId)));
  }

  return (
    <div className="card">
      <h2>Clients (users)</h2>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Tunnel IP</th>
            <th>Enabled</th>
            <th>DNS</th>
            <th>Access to nodes</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {clients.map((c) => (
            <tr key={c.id}>
              <td>{c.name}</td>
              <td className="mono">{c.address}</td>
              <td>
                <span
                  className={"toggle" + (c.enabled ? " on" : "")}
                  onClick={() => onChange(() => api.updateClient(c.id, !c.enabled, c.dns))}
                >
                  {c.enabled ? "● on" : "○ off"}
                </span>
              </td>
              <td className="mono">{c.dns || "(hub default)"}</td>
              <td>
                {nodes.map((n) => {
                  const on = c.granted_nodes.includes(n.id);
                  return (
                    <span
                      key={n.id}
                      className={"chip" + (on ? " on" : "")}
                      onClick={() => toggleGrant(c, n.id)}
                    >
                      {on ? "✓" : "＋"} {n.name}
                    </span>
                  );
                })}
                {nodes.length === 0 && <span className="mono">add a node first</span>}
              </td>
              <td className="actions">
                <button
                  className="ghost"
                  onClick={() =>
                    onConfig({
                      title: `Client: ${c.name}`,
                      url: api.clientConfigUrl(c.id),
                      filename: `${c.name}.conf`,
                    })
                  }
                >
                  Config / QR
                </button>
                <button className="danger" onClick={() => onChange(() => api.deleteClient(c.id))}>
                  Delete
                </button>
              </td>
            </tr>
          ))}
          {clients.length === 0 && (
            <tr>
              <td colSpan={6} className="mono">
                no clients yet
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <div className="row" style={{ marginTop: 16 }}>
        <div className="field" style={{ flex: 1 }}>
          <label>Name</label>
          <input type="text" value={name} onChange={(e) => setName(e.target.value)} placeholder="laptop" />
        </div>
        <div className="field" style={{ flex: 1 }}>
          <label>Custom DNS (optional, comma-separated)</label>
          <input
            type="text"
            value={dns}
            onChange={(e) => setDns(e.target.value)}
            placeholder="1.1.1.1, 8.8.8.8"
          />
        </div>
        <button onClick={add} disabled={!name}>
          Add client
        </button>
      </div>
    </div>
  );
}
