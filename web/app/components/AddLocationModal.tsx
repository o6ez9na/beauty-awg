"use client";

import { useState } from "react";
import { api } from "../lib/api";
import { humanError } from "../lib/errors";
import Modal, { ModalFooter } from "./Modal";

// Adding a location by hand is the fallback path — the installer script enrols
// most of them automatically. The network interface is the only field a normal
// person won't know, so it keeps its working default and hides under Advanced.
export default function AddLocationModal({
  onDone,
  onClose,
}: {
  onDone: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [subnets, setSubnets] = useState("");
  const [iface, setIface] = useState("eth0");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const ready = name.trim() && subnets.trim();

  async function save() {
    const list = subnets.split(",").map((s) => s.trim()).filter(Boolean);
    setBusy(true);
    setErr("");
    try {
      await api.createNode(name.trim(), iface.trim() || "eth0", list);
      onDone();
      onClose();
    } catch (e) {
      setErr(humanError(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Add a location"
      subtitle="A home or office whose network your devices should be able to reach."
      onClose={onClose}
    >
      {err && <div className="alert alert-error" role="alert">{err}</div>}

      <div className="field">
        <label htmlFor="loc-name">Name</label>
        <input
          id="loc-name"
          type="text"
          value={name}
          placeholder="Home"
          onChange={(e) => setName(e.target.value)}
        />
      </div>

      <div className="field">
        <label htmlFor="loc-subnets">Its local network</label>
        <input
          id="loc-subnets"
          type="text"
          value={subnets}
          placeholder="192.168.1.0/24"
          onChange={(e) => setSubnets(e.target.value)}
        />
        <p className="hint">
          The address range of the router at that place. It&rsquo;s usually printed on the
          router or shown in its admin page — most often 192.168.1.0/24 or 192.168.0.0/24.
          Two locations must not use the same one.
        </p>
      </div>

      <details className="advanced">
        <summary>Advanced</summary>
        <div className="field">
          <label htmlFor="loc-iface">Network interface on that machine</label>
          <input
            id="loc-iface"
            type="text"
            value={iface}
            onChange={(e) => setIface(e.target.value)}
          />
          <p className="hint">Leave this alone unless you know it&rsquo;s something other than eth0.</p>
        </div>
      </details>

      <ModalFooter>
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="btn" onClick={save} disabled={!ready || busy}>
          {busy ? "Adding…" : "Add location"}
        </button>
      </ModalFooter>
    </Modal>
  );
}
