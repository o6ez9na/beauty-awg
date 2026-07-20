"use client";

import { useState } from "react";
import { api, Client, Node } from "../lib/api";
import { markColor, readableTextColor } from "../lib/color";
import { configChanged } from "../lib/toast";
import { humanError } from "../lib/errors";

// Granting access used to require dragging an arrow between two cards in the
// network map — undiscoverable unless someone told you. The same operation is
// the whole point of the panel, so it lives here as a plain row of on/off
// toggles on the device itself. The map still works and stays in sync.
export default function AccessPicker({
  client,
  locations,
  groupName,
  onChanged,
  onError,
  onEditRules,
}: {
  client: Client;
  locations: Node[];
  /** Set when the device is in a group: its access is the group's, so it is
   *  shown read-only here and edited on the map instead. */
  groupName?: string;
  onChanged: () => void;
  onError: (msg: string) => void;
  onEditRules?: (nodeId: string) => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const managed = groupName !== undefined;

  async function toggle(node: Node, granted: boolean) {
    setBusy(node.id);
    onError("");
    try {
      if (granted) await api.revoke(client.id, node.id);
      else await api.grant(client.id, node.id);
      configChanged(client.name);
      onChanged();
    } catch (e) {
      onError(humanError(e));
    } finally {
      setBusy(null);
    }
  }

  if (locations.length === 0) {
    return (
      <p className="access-none">
        Nothing to connect to yet — add a location first and it will show up here.
      </p>
    );
  }

  return (
    <div className="access">
      <p className="access-label" id={`access-${client.id}`}>
        {managed ? `Can reach — managed by group “${groupName}”` : "Can reach"}
      </p>
      <div className="access-chips" role="group" aria-labelledby={`access-${client.id}`}>
        {locations.map((n) => {
          const granted = client.granted_nodes.includes(n.id);
          const label = n.is_hub ? "The internet" : n.name;
          const working = busy === n.id;
          // A grouped device's access follows its group. Show what it reaches but
          // don't offer to change it here — the toggle would only drift it away
          // from the group until the next sync pulls it back. Edit it on the map.
          if (managed && !granted) return null; // don't advertise ungranted ones read-only
          return (
            <span key={n.id} className={"achip" + (granted ? " on" : "") + (working ? " busy" : "") + (managed ? " locked" : "")}>
              <button
                type="button"
                className="achip-main"
                aria-pressed={granted}
                disabled={working || managed}
                onClick={() => { if (!managed) toggle(n, granted); }}
                title={
                  managed
                    ? `Managed by group “${groupName}” — edit access on the map`
                    : n.is_hub
                      ? "Send this device's internet traffic through the server"
                      : `Local network: ${n.subnets.join(", ") || "not set"}`
                }
              >
                <span
                  className="achip-mark"
                  aria-hidden="true"
                  style={granted ? { background: markColor(n), color: readableTextColor(markColor(n)) } : undefined}
                >
                  {granted ? "✓" : "+"}
                </span>
                <span className="achip-name">{label}</span>
              </button>
              {!managed && granted && !n.is_hub && onEditRules && (
                <button
                  type="button"
                  className="achip-gear"
                  onClick={() => onEditRules(n.id)}
                  aria-label={`Limit what ${client.name} can reach at ${n.name}`}
                  title="Limit to certain addresses or ports"
                >
                  ⚙
                </button>
              )}
            </span>
          );
        })}
        {managed && client.granted_nodes.length === 0 && (
          <span className="access-none-inline">Group grants no access yet</span>
        )}
      </div>
    </div>
  );
}
