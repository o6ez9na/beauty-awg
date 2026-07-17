"use client";

import { useState } from "react";

// Rename a client or node. Saves the trimmed name; empty is rejected.
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
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ textAlign: "left" }} onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>
        <div className="field">
          <label>Name</label>
          <input
            type="text"
            value={name}
            autoFocus
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") save(); if (e.key === "Escape") onClose(); }}
          />
        </div>
        <div className="row" style={{ marginTop: 16, justifyContent: "flex-end" }}>
          <button className="ghost" onClick={onClose}>Cancel</button>
          <button className="btn" onClick={save} disabled={!trimmed}>Save</button>
        </div>
      </div>
    </div>
  );
}
