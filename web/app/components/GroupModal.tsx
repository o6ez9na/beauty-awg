"use client";

import { useState } from "react";
import { Client } from "../lib/api";
import { Group } from "../lib/groups";
import Modal, { ModalFooter } from "./Modal";
import ConfirmModal from "./ConfirmModal";
import ColorPickerModal from "./ColorPickerModal";

// Rename a group, take devices out of it, or dissolve it. Removing a device
// here only takes it out of the group — its access is left exactly as it is,
// because silently stripping someone's access from a tidy-up action would be
// the opposite of what "remove from group" sounds like.
export default function GroupModal({
  group,
  members,
  onRename,
  onColor,
  onRemoveMember,
  onDissolve,
  onClose,
}: {
  group: Group;
  members: Client[];
  onRename: (name: string) => void;
  onColor: (color: string) => void;
  onRemoveMember: (clientId: string) => void;
  onDissolve: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(group.name);
  const [confirming, setConfirming] = useState(false);
  const [pickingColor, setPickingColor] = useState(false);

  function saveName() {
    const n = name.trim();
    if (n && n !== group.name) onRename(n);
    else setName(group.name);
  }

  return (
    <>
      <Modal
        title={group.name}
        subtitle="Devices in a group share one set of destinations."
        onClose={onClose}
      >
        <div className="field">
          <label htmlFor="group-name">Group name</label>
          <input
            id="group-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={saveName}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
          />
        </div>

        <h3 className="section-h">Devices</h3>
        {members.length === 0 ? (
          <p className="prose">
            Nothing in here yet. Drag a device card onto the group on the map to add it.
          </p>
        ) : (
          <ul className="picklist" role="list">
            {members.map((m) => (
              <li key={m.id}>
                <div className="pickrow">
                  <span className={"dot " + (m.online ? "live" : "")} aria-hidden="true" />
                  <span className="pickmain">
                    <span className="pickname">{m.name}</span>
                    <span className="picksub">{m.address}</span>
                  </span>
                  <button className="ghost" onClick={() => onRemoveMember(m.id)}>
                    Take out
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
        <p className="hint">
          Taking a device out leaves its access untouched — it just stops being managed
          together with the rest.
        </p>

        <ModalFooter>
          <button className="danger" onClick={() => setConfirming(true)}>Delete group</button>
          <button className="ghost" onClick={() => setPickingColor(true)}>Change color</button>
          <button className="ghost" onClick={onClose}>Close</button>
        </ModalFooter>
      </Modal>

      {pickingColor && (
        <ColorPickerModal
          title={`Color for ${group.name}`}
          current={group.color ?? ""}
          seed={group.id}
          onSave={onColor}
          onClose={() => setPickingColor(false)}
        />
      )}

      {confirming && (
        <ConfirmModal
          title={`Delete ${group.name}?`}
          body="The devices and their access stay exactly as they are — only the grouping goes away."
          confirmLabel="Delete group"
          onConfirm={onDissolve}
          onClose={() => setConfirming(false)}
        />
      )}
    </>
  );
}
