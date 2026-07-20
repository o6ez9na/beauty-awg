"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, Node, Client, NodeLink } from "./lib/api";
import { humanError } from "./lib/errors";
import ConfigModal from "./components/ConfigModal";
import AccessGraph from "./components/AccessGraph";
import ClientDetails from "./components/ClientDetails";
import RulesModal from "./components/RulesModal";
import DevicesView from "./components/DevicesView";
import LocationsView from "./components/LocationsView";
import AddDeviceWizard from "./components/AddDeviceWizard";
import AddLocationModal from "./components/AddLocationModal";
import Toaster from "./components/Toaster";
import ThemeToggle from "./components/ThemeToggle";

type Tab = "devices" | "locations" | "map";

const TABS: Tab[] = ["devices", "locations", "map"];
const isTab = (v: string): v is Tab => (TABS as string[]).includes(v);

// The frontend's own build id, stamped in at image build time. It only differs
// from the API's version on a from-source build, which is exactly when knowing
// it matters.
const UI_BUILD = process.env.NEXT_PUBLIC_APP_VERSION || "";

export default function Dashboard() {
  const router = useRouter();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [clients, setClients] = useState<Client[]>([]);
  const [links, setLinks] = useState<NodeLink[]>([]);
  const [tab, setTab] = useState<Tab>("devices");
  // Nothing of the panel is rendered until the first load comes back. A stale
  // cookie gets past the middleware, and painting the dashboard before finding
  // out it is not ours is exactly the flash this avoids.
  const [ready, setReady] = useState(false);
  const [err, setErr] = useState("");
  const [version, setVersion] = useState("");

  const [addingDevice, setAddingDevice] = useState(false);
  const [addingLocation, setAddingLocation] = useState(false);
  const [openDeviceId, setOpenDeviceId] = useState<string | null>(null);
  const [configNode, setConfigNode] = useState<Node | null>(null);
  const [editingRules, setEditingRules] = useState<{ clientId: string; nodeId: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [n, c, l] = await Promise.all([api.listNodes(), api.listClients(), api.listNodeLinks()]);
      setNodes(n || []);
      setClients(c || []);
      setLinks(l || []);
      setErr("");
      setReady(true);
    } catch (e) {
      // Stay unready on 401 so the redirect happens without a frame of panel.
      if (e instanceof ApiError && e.status === 401) return router.push("/login");
      setErr(humanError(e));
      setReady(true);
    }
  }, [router]);

  // Poll so new enrolment requests and online/offline changes show up live.
  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  useEffect(() => {
    api.getVersion().then((v) => setVersion(v.version)).catch(() => {});
  }, []);

  // Keep the open tab in the URL fragment so a reload (or a shared link) lands
  // back where you were instead of always on Devices.
  useEffect(() => {
    const fromUrl = window.location.hash.slice(1);
    if (isTab(fromUrl)) setTab(fromUrl);
  }, []);

  const selectTab = useCallback((t: Tab) => {
    setTab(t);
    // replaceState, not push: tabs shouldn't pile up in the back button.
    window.history.replaceState(null, "", `#${t}`);
  }, []);

  const guard = useCallback(
    async (fn: () => Promise<void>) => {
      setErr("");
      try {
        await fn();
        await load();
      } catch (e) {
        setErr(humanError(e));
      }
    },
    [load]
  );

  const activeNodes = nodes.filter((n) => n.status === "active");
  const homeNodes = activeNodes.filter((n) => !n.is_hub);
  const pending = nodes.filter((n) => n.status === "pending");

  // Show the UI build alongside the API version only when they disagree — on a
  // released image they're the same tag and one is enough.
  const uiDiffers = UI_BUILD !== "" && UI_BUILD !== "dev" && UI_BUILD !== version;
  const versionLabel = [version, uiDiffers ? `ui ${UI_BUILD}` : ""].filter(Boolean).join(" · ");
  const versionTitle = uiDiffers
    ? `API ${version || "unknown"} · web UI built from ${UI_BUILD}`
    : `Panel version ${version}`;

  const openDevice = openDeviceId ? clients.find((c) => c.id === openDeviceId) ?? null : null;
  const rulesClient = editingRules ? clients.find((c) => c.id === editingRules.clientId) : null;
  const rulesNode = editingRules ? activeNodes.find((n) => n.id === editingRules.nodeId) : null;

  if (!ready) {
    return (
      <div className="boot" role="status" aria-live="polite">
        <img src="/logo.svg" alt="" width={32} height={32} />
        <span>Loading…</span>
      </div>
    );
  }

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <img src="/logo.svg" alt="" className="brand-logo" />
          <span>6ers3<b>rk</b></span>
        </div>

        <nav className="tabs" aria-label="Sections">
          <button className="tab" aria-current={tab === "devices"} onClick={() => selectTab("devices")}>
            Devices <span className="tab-count">{clients.length}</span>
          </button>
          <button className="tab" aria-current={tab === "locations"} onClick={() => selectTab("locations")}>
            Locations <span className="tab-count">{homeNodes.length}</span>
            {pending.length > 0 && (
              <span className="tab-badge" aria-label={`${pending.length} waiting for approval`}>
                {pending.length}
              </span>
            )}
          </button>
          <button className="tab" aria-current={tab === "map"} onClick={() => selectTab("map")}>
            Network map
          </button>
        </nav>

        <div className="spacer" />
        {versionLabel && (
          <span className="version-tag" title={versionTitle}>{versionLabel}</span>
        )}
        <ThemeToggle />
        <button className="ghost" onClick={() => api.logout().then(() => router.push("/login"))}>
          Sign out
        </button>
      </header>

      <main className={tab === "map" ? "stage" : "content"}>
        {err && (
          <div className="alert alert-error" role="alert">
            {err}
            <button className="alert-x" onClick={() => setErr("")} aria-label="Dismiss">✕</button>
          </div>
        )}

        {tab === "devices" && (
          <>
            <div className="viewhead">
              <div>
                <h1>Devices</h1>
                <p className="viewsub">Each phone, laptop or tablet that connects gets its own config.</p>
              </div>
              <button className="btn" onClick={() => setAddingDevice(true)}>Add device</button>
            </div>
            <DevicesView
              clients={clients}
              locations={activeNodes}
              onChange={guard}
              onError={setErr}
              onOpen={(c) => setOpenDeviceId(c.id)}
              onAdd={() => setAddingDevice(true)}
              onEditRules={(clientId, nodeId) => setEditingRules({ clientId, nodeId })}
            />
          </>
        )}

        {tab === "locations" && (
          <>
            <div className="viewhead">
              <div>
                <h1>Locations</h1>
                <p className="viewsub">Homes and offices whose local network your devices can reach.</p>
              </div>
              <button className="btn" onClick={() => setAddingLocation(true)}>Add location</button>
            </div>
            <LocationsView
              nodes={nodes}
              onChange={guard}
              onConfig={setConfigNode}
              onAdd={() => setAddingLocation(true)}
            />
          </>
        )}

        {tab === "map" && (
          <AccessGraph
            nodes={nodes}
            clients={clients}
            links={links}
            onChanged={load}
            onError={setErr}
          />
        )}
      </main>

      <Toaster />

      {addingDevice && (
        <AddDeviceWizard
          locations={activeNodes}
          onDone={load}
          onClose={() => setAddingDevice(false)}
        />
      )}

      {addingLocation && (
        <AddLocationModal onDone={load} onClose={() => setAddingLocation(false)} />
      )}

      {openDevice && (
        <ClientDetails
          client={openDevice}
          onRename={(nm) => guard(() => api.renameClient(openDevice.id, nm))}
          onColor={(color) => guard(() => api.setClientColor(openDevice.id, color))}
          onDelete={() => { guard(() => api.deleteClient(openDevice.id)); setOpenDeviceId(null); }}
          onClose={() => setOpenDeviceId(null)}
        />
      )}

      {editingRules && rulesClient && rulesNode && (
        <RulesModal
          clientIds={[editingRules.clientId]}
          nodeId={editingRules.nodeId}
          clientName={rulesClient.name}
          nodeName={rulesNode.name}
          subnetHints={rulesNode.subnets}
          onRevoke={() => {
            guard(() => api.revoke(editingRules.clientId, editingRules.nodeId));
            setEditingRules(null);
          }}
          onClose={() => { setEditingRules(null); load(); }}
        />
      )}

      {configNode && (
        <ConfigModal
          title={configNode.name}
          url={api.nodeConfigUrl(configNode.id)}
          filename={`${configNode.name}.conf`}
          onClose={() => setConfigNode(null)}
        />
      )}
    </div>
  );
}
