"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, Node, Client, NodeLink } from "./lib/api";
import ConfigModal from "./components/ConfigModal";
import AccessGraph from "./components/AccessGraph";
import ConfirmModal from "./components/ConfirmModal";
import RenameModal from "./components/RenameModal";
import ClientDetails from "./components/ClientDetails";
import ColorPickerModal from "./components/ColorPickerModal";
import Toaster from "./components/Toaster";
import { toast } from "./lib/toast";

type Modal = { title: string; url: string; filename: string; vpnLinkUrl?: string };

export default function Dashboard() {
  const router = useRouter();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [clients, setClients] = useState<Client[]>([]);
  const [links, setLinks] = useState<NodeLink[]>([]);
  const [tab, setTab] = useState<"nodes" | "clients">("clients");
  const [err, setErr] = useState("");
  const [modal, setModal] = useState<Modal | null>(null);
  const [version, setVersion] = useState("");
  const [selectedClient, setSelectedClient] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [n, c, l] = await Promise.all([api.listNodes(), api.listClients(), api.listNodeLinks()]);
      setNodes(n || []);
      setClients(c || []);
      setLinks(l || []);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return router.push("/login");
      setErr(String(e));
    }
  }, [router]);

  // Poll so new enrollment requests and online/offline changes show up live.
  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  useEffect(() => {
    api.getVersion().then((v) => setVersion(v.version)).catch(() => {});
  }, []);

  async function guard(fn: () => Promise<void>) {
    setErr("");
    try {
      await fn();
      await load();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    }
  }

  const activeNodes = nodes.filter((n) => n.status === "active" && !n.is_hub);
  const pending = nodes.filter((n) => n.status === "pending");

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <img src="/logo.svg" alt="" className="brand-logo" />
          <span>6ers3<b>rk</b></span>
          {version && <span className="brand-version">{version}</span>}
        </div>
        <div className="meshstat">
          <span><b>{clients.length}</b> clients</span>
          <span><b>{activeNodes.length}</b> nodes</span>
          {pending.length > 0 && <span style={{ color: "var(--warn)" }}><b>{pending.length}</b> pending</span>}
        </div>
        <div className="spacer" />
        <button className="ghost" onClick={() => api.logout().then(() => router.push("/login"))}>
          Log out
        </button>
      </header>

      <aside className="rail">
        <div className="rail-body">
          <div className="seg" role="tablist">
            <button className="seg-btn" role="tab" aria-selected={tab === "clients"} onClick={() => setTab("clients")}>
              Clients <span className="seg-count">{clients.length}</span>
            </button>
            <button className="seg-btn" role="tab" aria-selected={tab === "nodes"} onClick={() => setTab("nodes")}>
              Nodes <span className="seg-count">{activeNodes.length}</span>
            </button>
          </div>

          {err && <div className="error">{err}</div>}

          {tab === "clients" ? (
            <ClientsTab
              clients={clients}
              selected={selectedClient}
              onSelect={setSelectedClient}
              onChange={guard}
            />
          ) : (
            <NodesTab nodes={nodes} onChange={guard} onConfig={setModal} />
          )}
        </div>
      </aside>

      <main className="stage">
        <AccessGraph
          nodes={nodes}
          clients={clients}
          links={links}
          onChanged={load}
          selectedClientId={selectedClient}
          onError={setErr}
        />
      </main>

      <Toaster />

      {modal && (
        <ConfigModal
          title={modal.title}
          url={modal.url}
          filename={modal.filename}
          vpnLinkUrl={modal.vpnLinkUrl}
          onClose={() => setModal(null)}
        />
      )}
    </div>
  );
}

/* ---------------- Clients ---------------- */

function ClientsTab({
  clients, selected, onSelect, onChange,
}: {
  clients: Client[];
  selected: string | null;
  onSelect: (id: string | null) => void;
  onChange: (fn: () => Promise<void>) => void;
}) {
  const [name, setName] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);

  const open = openId ? clients.find((c) => c.id === openId) ?? null : null;

  function add() {
    const n = name.trim();
    if (!n) return;
    // New clients always use the hub resolver (10.8.0.1) — no custom DNS.
    onChange(async () => { await api.createClient(n, ""); setName(""); });
  }

  return (
    <>
      <p className="eyebrow">People who connect</p>
      <div className="rows">
        {clients.map((c) => (
          <div
            key={c.id}
            className={"item" + (c.id === selected ? " selected" : "")}
            onClick={() => { onSelect(c.id); setOpenId(c.id); }}
            style={{ cursor: "pointer" }}
          >
            <div className="item-head">
              <span className={"dot " + (c.online ? "live" : "")} title={c.online ? "online" : "offline"} />
              <span className="item-name">{c.name}</span>
              <span className="item-ip">{c.address}</span>
            </div>
            <div className="item-meta">
              <span className="k">access</span>{" "}
              {c.granted_nodes.length ? `${c.granted_nodes.length} node(s)` : "none yet — drag an arrow in the graph"}
            </div>
            <div className="item-actions" onClick={(e) => e.stopPropagation()}>
              <span
                className="switch-wrap"
                role="switch"
                aria-checked={c.enabled}
                onClick={() => onChange(() => api.updateClient(c.id, !c.enabled, ""))}
              >
                <span className={"switch" + (c.enabled ? " on" : "")} />
                <span className={"switch-label" + (c.enabled ? " on" : "")}>
                  {c.enabled ? "enabled" : "disabled"}
                </span>
              </span>
            </div>
          </div>
        ))}
        {clients.length === 0 && <div className="empty">No clients yet. Add one below.</div>}
      </div>

      {open && (
        <ClientDetails
          client={open}
          onRename={(nm) => onChange(() => api.renameClient(open.id, nm))}
          onColor={(color) => onChange(() => api.setClientColor(open.id, color))}
          onDelete={() => { onChange(() => api.deleteClient(open.id)); setOpenId(null); }}
          onClose={() => setOpenId(null)}
        />
      )}

      <div className="addbox">
        <p className="eyebrow" style={{ margin: 0 }}>Add a client</p>
        <div className="field">
          <label>Name</label>
          <input
            type="text" value={name} placeholder="laptop"
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
          />
        </div>
        <div className="mono" style={{ fontSize: 11, color: "var(--muted)" }}>
          DNS is set to the hub resolver automatically.
        </div>
        <button className="btn" onClick={add} disabled={!name.trim()}>Add client</button>
      </div>
    </>
  );
}

/* ---------------- Nodes ---------------- */

function NodesTab({
  nodes, onChange, onConfig,
}: {
  nodes: Node[];
  onChange: (fn: () => Promise<void>) => void;
  onConfig: (m: Modal) => void;
}) {
  const [name, setName] = useState("");
  const [iface, setIface] = useState("eth0");
  const [subnets, setSubnets] = useState("");
  const [confirmDel, setConfirmDel] = useState<Node | null>(null);
  const [renaming, setRenaming] = useState<Node | null>(null);
  const [pickingColor, setPickingColor] = useState<Node | null>(null);

  const pending = nodes.filter((n) => n.status === "pending");
  const active = nodes.filter((n) => n.status === "active");

  function add() {
    const list = subnets.split(",").map((s) => s.trim()).filter(Boolean);
    if (!name.trim() || !list.length) return;
    onChange(async () => { await api.createNode(name.trim(), iface, list); setName(""); setSubnets(""); });
  }

  return (
    <>
      {pending.length > 0 && (
        <>
          <p className="eyebrow">Waiting for approval</p>
          {pending.map((n) => (
            <div key={n.id} className="pending">
              <div className="pending-title">enrollment request</div>
              <div className="item-head">
                <span className="item-name">{n.name}</span>
                <span className="item-ip">{n.hostname}</span>
              </div>
              <div className="item-meta">wants {n.subnets.join(", ")} · iface {n.lan_iface}</div>
              <div className="item-actions">
                <button className="btn" onClick={() => onChange(() => api.approveNode(n.id))}>Approve</button>
                <button className="danger" onClick={() => onChange(() => api.rejectNode(n.id))}>Reject</button>
              </div>
            </div>
          ))}
        </>
      )}

      <p className="eyebrow">Home servers</p>
      <div className="rows">
        {active.map((n) => (
          <div key={n.id} className={"item" + (n.is_hub ? " exit" : "")}>
            <div className="item-head">
              {!n.is_hub && <span className={"dot " + (n.online ? "live" : "")} title={n.online ? "online" : "offline"} />}
              <span className="item-name">{n.is_hub ? "◍ internet exit" : n.name}</span>
              <span className="item-ip">{n.address}</span>
            </div>
            {n.is_hub ? (
              <>
                <div className="item-meta"><span className="k">routes</span> 0.0.0.0/0 · the panel itself</div>
                <div className="item-actions">
                  <button className="ghost" onClick={() => setPickingColor(n)}>Color</button>
                </div>
              </>
            ) : (
              <>
                <div className="item-meta">
                  <span className="k">lan</span> {n.subnets.join(", ")} · {n.lan_iface}
                  {n.last_seen && <> · <span className="k">seen</span> {new Date(n.last_seen).toLocaleString()}</>}
                </div>
                <NodeNet node={n} onChange={onChange} />
                <div className="item-actions">
                  <button
                    className="ghost"
                    onClick={() => onConfig({ title: n.name, url: api.nodeConfigUrl(n.id), filename: `${n.name}.conf` })}
                  >
                    Config
                  </button>
                  <button className="ghost" onClick={() => setRenaming(n)}>Rename</button>
                  <button className="ghost" onClick={() => setPickingColor(n)}>Color</button>
                  <button className="danger" style={{ marginLeft: "auto" }} onClick={() => setConfirmDel(n)}>
                    Delete
                  </button>
                </div>
              </>
            )}
          </div>
        ))}
        {active.length === 0 && <div className="empty">No nodes yet. Add one below, or run the node installer.</div>}
      </div>

      {confirmDel && (
        <ConfirmModal
          title={`Delete ${confirmDel.name}?`}
          body="Removes the node and all access grants to it. This cannot be undone."
          onConfirm={() => {
            onChange(() => api.deleteNode(confirmDel.id));
            toast(`Node ${confirmDel.name} deleted — re-share configs of clients that had access`, "warn");
          }}
          onClose={() => setConfirmDel(null)}
        />
      )}
      {renaming && (
        <RenameModal
          title="Rename node"
          current={renaming.name}
          onSave={(nm) => onChange(() => api.renameNode(renaming.id, nm))}
          onClose={() => setRenaming(null)}
        />
      )}
      {pickingColor && (
        <ColorPickerModal
          title={`Color — ${pickingColor.is_hub ? "internet exit" : pickingColor.name}`}
          current={pickingColor.color}
          seed={pickingColor.address || pickingColor.id}
          onSave={(color) => onChange(() => api.setNodeColor(pickingColor.id, color))}
          onClose={() => setPickingColor(null)}
        />
      )}

      <div className="addbox">
        <p className="eyebrow" style={{ margin: 0 }}>Add a node manually</p>
        <div className="field">
          <label>Name</label>
          <input type="text" value={name} placeholder="home1" onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label>LAN subnets (comma-separated)</label>
          <input type="text" value={subnets} placeholder="192.168.1.0/24" onChange={(e) => setSubnets(e.target.value)} />
        </div>
        <div className="field">
          <label>LAN interface</label>
          <input type="text" value={iface} onChange={(e) => setIface(e.target.value)} />
        </div>
        <button className="btn" onClick={add} disabled={!name.trim() || !subnets.trim()}>Add node</button>
      </div>
    </>
  );
}

// DNS server + local domains for a node, saved together so the hub resolver
// forwards those domains to this node's DNS.
function NodeNet({ node, onChange }: { node: Node; onChange: (fn: () => Promise<void>) => void }) {
  const [dns, setDns] = useState(node.dns);
  const [domains, setDomains] = useState(node.domains.join(", "));

  function save() {
    const list = domains.split(",").map((s) => s.trim()).filter(Boolean);
    if (dns !== node.dns || list.join(",") !== node.domains.join(",")) {
      onChange(() => api.updateNode(node.id, dns, list));
      toast(`Node ${node.name} DNS changed — re-share configs of clients with access`, "warn");
    }
  }

  return (
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8, marginTop: 10 }}>
      <div className="field">
        <label>DNS server</label>
        <input type="text" value={dns} placeholder="192.168.1.1" onChange={(e) => setDns(e.target.value)} onBlur={save} />
      </div>
      <div className="field">
        <label>Local domains</label>
        <input type="text" value={domains} placeholder="home.lan" onChange={(e) => setDomains(e.target.value)} onBlur={save} />
      </div>
    </div>
  );
}
