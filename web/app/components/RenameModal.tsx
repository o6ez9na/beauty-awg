"use client";

import { useState } from "react";
import Modal, { ModalFooter } from "./Modal";

// Rename a device or location. Saves the trimmed name; empty is rejected.
export default function RenameModal({
  title,
  current,
  onSave,
  onClose,
}: {
  title: string;
  current: string;
  onSave: (name: string) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(current);
  const trimmed = name.trim();

  function save() {
    if (!trimmed || trimmed === current) { onClose(); return; }
    onSave(trimmed);
    onClose();
  }

  return (
    <Modal title={title} onClose={onClose}>
      <div className="field">
        <label htmlFor="rename-input">Name</label>
        <input
          id="rename-input"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") save(); }}
        />
      </div>
      <ModalFooter>
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="btn" onClick={save} disabled={!trimmed}>Save</button>
      </ModalFooter>
    </Modal>
  );
}
