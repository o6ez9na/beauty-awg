"use client";

import { api, Client, Node } from "../lib/api";
import AccessPicker from "./AccessPicker";
import EmptyState from "./EmptyState";

// The device list is the panel's home screen: who connects, what each one is
// allowed to reach, and how to hand over the config. Everything a normal owner
// does day to day is on this screen without opening anything else.
export default function DevicesView({
  clients,
  locations,
  groupNameByClient,
  onChange,
  onError,
  onOpen,
  onAdd,
  onEditRules,
}: {
  clients: Client[];
  locations: Node[];
  /** clientId -> group name, for devices whose access is managed by a group. */
  groupNameByClient: Map<string, string>;
  onChange: (fn: () => Promise<void>) => void;
  onError: (msg: string) => void;
  onOpen: (client: Client) => void;
  onAdd: () => void;
  onEditRules: (clientId: string, nodeId: string) => void;
}) {
  if (clients.length === 0) {
    return (
      <EmptyState
        icon="📱"
        title="No devices yet"
        body="Add a phone, laptop or tablet and you'll get a QR code to connect it in seconds."
        action={<button className="btn" onClick={onAdd}>Add your first device</button>}
      />
    );
  }

  return (
    <div className="cardgrid">
      {clients.map((c) => (
        <article key={c.id} className={"card" + (c.enabled ? "" : " card-off")}>
          <div className="card-head">
            <span className={"dot " + (c.online ? "live" : "")} aria-hidden="true" />
            <button className="card-title" onClick={() => onOpen(c)}>
              {c.name}
            </button>
            <span className="card-addr">{c.address}</span>
          </div>

          <p className="card-status">
            {!c.enabled
              ? "Turned off — can't connect"
              : c.online
                ? "Connected now"
                : "Not connected right now"}
          </p>

          <AccessPicker
            client={c}
            locations={locations}
            groupName={groupNameByClient.get(c.id)}
            onChanged={() => onChange(async () => {})}
            onError={onError}
            onEditRules={(nodeId) => onEditRules(c.id, nodeId)}
          />

          <div className="card-foot">
            <button className="btn" onClick={() => onOpen(c)}>Get config</button>
            <button
              type="button"
              className={"switch-wrap" + (c.enabled ? " on" : "")}
              role="switch"
              aria-checked={c.enabled}
              aria-label={`${c.name} allowed to connect`}
              onClick={() => onChange(() => api.updateClient(c.id, !c.enabled, ""))}
            >
              <span className={"switch" + (c.enabled ? " on" : "")} aria-hidden="true" />
              <span className="switch-label">{c.enabled ? "On" : "Off"}</span>
            </button>
          </div>
        </article>
      ))}
    </div>
  );
}
