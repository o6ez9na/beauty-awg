"use client";

import { useState } from "react";
import { api, Node } from "../lib/api";
import { markColor } from "../lib/color";
import { humanError } from "../lib/errors";
import Modal, { ModalFooter } from "./Modal";
import SharePanel from "./SharePanel";

type Step = "name" | "access" | "share";

// "Add a device" is the one flow a non-technical owner runs over and over, so
// it's a guided three-step instead of a bare name field: name it, tick what it
// may reach, then hand over the QR — which is the actual goal, and previously
// took three more clicks in a separate window to find.
export default function AddDeviceWizard({
  locations,
  onDone,
  onClose,
}: {
  locations: Node[];
  /** createdId is passed on the step that creates the device, so a caller can
   *  place the new card where the user asked for it. */
  onDone: (createdId?: string) => void;
  onClose: () => void;
}) {
  const [step, setStep] = useState<Step>("name");
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [created, setCreated] = useState<{ id: string; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const trimmed = name.trim();

  function togglePick(id: string) {
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  }

  async function create() {
    setBusy(true);
    setErr("");
    try {
      // New devices always use the hub resolver, so no DNS is passed.
      const res = await api.createClient(trimmed, "");
      // Grants are applied one at a time; a failure part-way still leaves a
      // usable device, so report it rather than rolling back behind the scenes.
      for (const nodeId of picked) {
        await api.grant(res.id, nodeId);
      }
      setCreated({ id: res.id, name: trimmed });
      setStep("share");
      onDone(res.id);
    } catch (e) {
      setErr(humanError(e));
    } finally {
      setBusy(false);
    }
  }

  function finish() {
    onDone();
    onClose();
  }

  if (step === "share" && created) {
    return (
      <Modal
        title={`${created.name} is ready`}
        subtitle="Send this to the device to connect it."
        size="md"
        onClose={finish}
      >
        <SharePanel clientId={created.id} clientName={created.name} />
        <ModalFooter>
          <button className="btn" onClick={finish}>Done</button>
        </ModalFooter>
      </Modal>
    );
  }

  if (step === "access") {
    return (
      <Modal
        title="What can it reach?"
        subtitle="You can change this at any time later."
        size="sm"
        onClose={onClose}
      >
        {err && <div className="alert alert-error" role="alert">{err}</div>}

        {locations.length === 0 ? (
          <p className="hint">
            No locations set up yet. You can add this device now and give it access once you
            have one.
          </p>
        ) : (
          <ul className="picklist" role="list">
            {locations.map((n) => {
              const on = picked.includes(n.id);
              return (
                <li key={n.id}>
                  <label className={"pickrow" + (on ? " on" : "")}>
                    <input type="checkbox" checked={on} onChange={() => togglePick(n.id)} />
                    <span className="pickdot" style={{ background: markColor(n) }} aria-hidden="true" />
                    <span className="pickmain">
                      <span className="pickname">{n.is_hub ? "The internet" : n.name}</span>
                      <span className="picksub">
                        {n.is_hub
                          ? "Send all browsing through the server"
                          : n.subnets.join(", ") || "no local range set"}
                      </span>
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        )}

        <ModalFooter>
          <button className="ghost" onClick={() => setStep("name")} disabled={busy}>Back</button>
          <button className="btn" onClick={create} disabled={busy}>
            {busy ? "Creating…" : "Create device"}
          </button>
        </ModalFooter>
      </Modal>
    );
  }

  return (
    <Modal
      title="Add a device"
      subtitle="One config per phone, laptop or tablet that should connect."
      size="sm"
      onClose={onClose}
    >
      {err && <div className="alert alert-error" role="alert">{err}</div>}
      <div className="field">
        <label htmlFor="new-device-name">Name it</label>
        <input
          id="new-device-name"
          type="text"
          value={name}
          placeholder="Anna's phone"
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && trimmed && setStep("access")}
        />
        <p className="hint">Just so you can tell them apart later.</p>
      </div>
      <ModalFooter>
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="btn" onClick={() => setStep("access")} disabled={!trimmed}>
          Next
        </button>
      </ModalFooter>
    </Modal>
  );
}
