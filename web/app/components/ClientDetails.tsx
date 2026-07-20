"use client";

import { useState } from "react";
import { Client } from "../lib/api";
import ConfirmModal from "./ConfirmModal";
import ColorPickerModal from "./ColorPickerModal";
import Modal, { ModalFooter } from "./Modal";
import SharePanel from "./SharePanel";

// The window for one device: rename it, hand out its config, or remove it.
// Opened from the device list and from the network map, so it owns no data of
// its own — every change goes back out through the callbacks.
export default function ClientDetails({
  client,
  onRename,
  onColor,
  onDelete,
  onClose,
}: {
  client: Client;
  onRename: (name: string) => void;
  onColor: (color: string) => void;
  onDelete: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(client.name);
  const [confirming, setConfirming] = useState(false);
  const [pickingColor, setPickingColor] = useState(false);

  function saveName() {
    const n = name.trim();
    if (n && n !== client.name) onRename(n);
    else setName(client.name);
  }

  return (
    <>
      <Modal
        title={client.name}
        subtitle={
          <>
            <span className={"dot " + (client.online ? "live" : "")} aria-hidden="true" />{" "}
            {client.online ? "Connected now" : "Not connected"} · {client.address}
            {!client.enabled && " · turned off"}
          </>
        }
        size="md"
        onClose={onClose}
      >
        <div className="field">
          <label htmlFor={`dev-name-${client.id}`}>Name</label>
          <input
            id={`dev-name-${client.id}`}
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={saveName}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
          />
        </div>

        <h3 className="section-h">Connect this device</h3>
        <SharePanel clientId={client.id} clientName={client.name} />

        <ModalFooter>
          <button className="ghost" onClick={() => setPickingColor(true)}>Change color</button>
          <button className="danger" onClick={() => setConfirming(true)}>Remove device</button>
        </ModalFooter>
      </Modal>

      {confirming && (
        <ConfirmModal
          title={`Remove ${client.name}?`}
          body="It will stop connecting immediately and its config will no longer work. This can't be undone."
          confirmLabel="Remove device"
          onConfirm={onDelete}
          onClose={() => setConfirming(false)}
        />
      )}

      {pickingColor && (
        <ColorPickerModal
          title={`Color for ${client.name}`}
          current={client.color}
          seed={client.address || client.id}
          onSave={onColor}
          onClose={() => setPickingColor(false)}
        />
      )}
    </>
  );
}
