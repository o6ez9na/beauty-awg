"use client";

import { useState } from "react";
import { api, Node } from "../lib/api";
import { toast } from "../lib/toast";
import ConfirmModal from "./ConfirmModal";
import RenameModal from "./RenameModal";
import ColorPickerModal from "./ColorPickerModal";
import EmptyState from "./EmptyState";
import LocationSettings from "./LocationSettings";

// Places whose local network the devices can reach. Enrolment requests are
// pulled to the top as a decision to make, not a status to read past.
export default function LocationsView({
  nodes,
  onChange,
  onConfig,
  onAdd,
}: {
  nodes: Node[];
  onChange: (fn: () => Promise<void>) => void;
  onConfig: (node: Node) => void;
  onAdd: () => void;
}) {
  const [confirmDel, setConfirmDel] = useState<Node | null>(null);
  const [renaming, setRenaming] = useState<Node | null>(null);
  const [pickingColor, setPickingColor] = useState<Node | null>(null);

  const pending = nodes.filter((n) => n.status === "pending");
  const active = nodes.filter((n) => n.status === "active");
  const real = active.filter((n) => !n.is_hub);

  return (
    <>
      {pending.length > 0 && (
        <section className="approvals">
          <h2 className="section-h">Waiting for you to approve</h2>
          {pending.map((n) => (
            <article key={n.id} className="card card-pending">
              <div className="card-head">
                <span className="card-title as-text">{n.name}</span>
                <span className="card-addr">{n.hostname}</span>
              </div>
              <p className="prose">
                A machine is asking to join and share its local network{" "}
                <b>{n.subnets.join(", ")}</b>. Approve it only if you set it up yourself.
              </p>
              <div className="card-foot">
                <button className="btn" onClick={() => onChange(() => api.approveNode(n.id))}>
                  Approve
                </button>
                <button className="danger" onClick={() => onChange(() => api.rejectNode(n.id))}>
                  Reject
                </button>
              </div>
            </article>
          ))}
        </section>
      )}

      {active.length === 0 ? (
        <EmptyState
          icon="🏠"
          title="No locations yet"
          body="A location is a home or office running the node installer. Once added, your devices can reach everything on its network."
          action={<button className="btn" onClick={onAdd}>Add a location</button>}
        />
      ) : (
        <div className="cardgrid">
          {active.map((n) =>
            n.is_hub ? (
              <article key={n.id} className="card card-exit">
                <div className="card-head">
                  <span className="card-title as-text">The internet</span>
                  <span className="card-addr">{n.address}</span>
                </div>
                <p className="prose">
                  Send a device&rsquo;s whole internet connection through this server. Useful on
                  public Wi-Fi. Turn it on per device in the Devices tab.
                </p>
                <div className="card-foot">
                  <button className="ghost" onClick={() => setPickingColor(n)}>Change color</button>
                </div>
              </article>
            ) : (
              <article key={n.id} className="card">
                <div className="card-head">
                  <span className={"dot " + (n.online ? "live" : "")} aria-hidden="true" />
                  <span className="card-title as-text">{n.name}</span>
                  <span className="card-addr">{n.address}</span>
                </div>
                <p className="card-status">
                  {n.online ? "Online" : "Offline"}
                  {n.last_seen && !n.online && ` · last seen ${new Date(n.last_seen).toLocaleString()}`}
                </p>
                <p className="card-detail">
                  Shares <b>{n.subnets.join(", ") || "nothing yet"}</b>
                </p>

                <LocationSettings node={n} onChange={onChange} />

                <div className="card-foot">
                  <button className="btn" onClick={() => onConfig(n)}>Setup file</button>
                  <button className="ghost" onClick={() => setRenaming(n)}>Rename</button>
                  <button className="ghost" onClick={() => setPickingColor(n)}>Color</button>
                  <button className="danger" onClick={() => setConfirmDel(n)}>Remove</button>
                </div>
              </article>
            )
          )}
        </div>
      )}

      {active.length > 0 && (
        <div className="viewfoot">
          <button className="ghost" onClick={onAdd}>Add another location</button>
          {real.length === 0 && (
            <p className="hint">
              You have no home or office locations yet — only the internet exit.
            </p>
          )}
        </div>
      )}

      {confirmDel && (
        <ConfirmModal
          title={`Remove ${confirmDel.name}?`}
          body="Devices will lose access to this network immediately, and every config that used it has to be shared again. This can't be undone."
          confirmLabel="Remove location"
          onConfirm={() => {
            onChange(() => api.deleteNode(confirmDel.id));
            toast(`${confirmDel.name} removed — share the configs again for devices that used it`, "warn");
          }}
          onClose={() => setConfirmDel(null)}
        />
      )}
      {renaming && (
        <RenameModal
          title={`Rename ${renaming.name}`}
          current={renaming.name}
          onSave={(nm) => onChange(() => api.renameNode(renaming.id, nm))}
          onClose={() => setRenaming(null)}
        />
      )}
      {pickingColor && (
        <ColorPickerModal
          title={`Color for ${pickingColor.is_hub ? "the internet exit" : pickingColor.name}`}
          current={pickingColor.color}
          seed={pickingColor.address || pickingColor.id}
          onSave={(color) => onChange(() => api.setNodeColor(pickingColor.id, color))}
          onClose={() => setPickingColor(null)}
        />
      )}
    </>
  );
}
