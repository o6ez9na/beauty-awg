"use client";

import { useEffect, useState } from "react";
import { api, Rule } from "../lib/api";
import { configChanged } from "../lib/toast";
import { humanError } from "../lib/errors";
import Modal, { ModalFooter } from "./Modal";

// Typing this as a destination means "everything", which is what the exit
// toggle already expresses — so it flips the toggle instead of standing as a
// rule of its own.
const CATCH_ALL = "0.0.0.0/0";

// Narrows one device's access at one location down to specific addresses and
// ports. Empty list = the whole network, which is the common case, so the
// screen leads with that fact instead of an empty table.

export default function RulesModal({
  clientIds,
  nodeId,
  clientName,
  nodeName,
  subnetHints,
  onClose,
  onRevoke,
}: {
  /** One device, or every member of a group. The editor reads the first one and
   *  writes the result to all of them, which is what makes a group's single line
   *  editable at all. */
  clientIds: string[];
  nodeId: string;
  clientName: string;
  nodeName: string;
  subnetHints: string[];
  onClose: () => void;
  onRevoke?: () => void;
}) {
  const [rules, setRules] = useState<Rule[]>([]);
  const [exit, setExit] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  // The first member stands for the group: membership keeps their access in
  // step, so any of them describes the whole.
  const lead = clientIds[0];

  useEffect(() => {
    if (!lead) return;
    api
      .getGrantRules(lead, nodeId)
      .then((r) => setRules(r || []))
      .catch((e) => setErr(humanError(e)))
      .finally(() => setLoaded(true));
    api.getGrantExit(lead, nodeId).then((r) => setExit(r.exit)).catch(() => {});
  }, [lead, nodeId]);

  const hasCatchAll = rules.some((r) => r.dest.trim() === CATCH_ALL);

  // Reaching 0.0.0.0/0 and routing all traffic through the location are the same
  // request, so entering one turns on the other.
  useEffect(() => {
    if (hasCatchAll) setExit(true);
  }, [hasCatchAll]);

  function update(i: number, patch: Partial<Rule>) {
    setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  }
  function addRule() {
    setRules((rs) => [...rs, { dest: subnetHints[0] || "", proto: "any", port_from: 0, port_to: 0 }]);
  }
  function removeRule(i: number) {
    setRules((rs) => rs.filter((_, j) => j !== i));
  }

  async function save() {
    setBusy(true);
    setErr("");
    try {
      // A catch-all alongside the exit flag would add a redundant, contradictory
      // accept to the ACL, so drop it — the toggle now carries that meaning.
      // Narrower rules are kept: they come back when the exit is turned off.
      const toSave = exit ? rules.filter((r) => r.dest.trim() !== CATCH_ALL) : rules;
      for (const id of clientIds) {
        // Exit first: it enforces the single-exit-location rule and may reject.
        await api.setGrantExit(id, nodeId, exit);
        await api.setGrantRules(id, nodeId, toSave);
      }
      configChanged(clientName);
      onClose();
    } catch (e) {
      setErr(humanError(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title={`${clientName} at ${nodeName}`}
      subtitle="Choose how much of this location the device is allowed to see."
      size="lg"
      onClose={onClose}
    >
      {err && <div className="alert alert-error" role="alert">{err}</div>}

      <button
        type="button"
        className={"toggle-row" + (exit ? " on" : "")}
        role="switch"
        aria-checked={exit}
        onClick={() => setExit((v) => !v)}
      >
        <span className={"switch" + (exit ? " on" : "")} aria-hidden="true" />
        <span className="toggle-text">
          <span className="toggle-title">Browse the internet through {nodeName}</span>
          <span className="toggle-sub">
            Websites will see {nodeName}&rsquo;s home internet address instead of the device&rsquo;s own.
          </span>
        </span>
      </button>

      <h3 className="section-h">What it can open</h3>

      {exit && (
        <p className="prose">
          Not used while everything goes through {nodeName} — that already covers the whole
          network. Turn the switch off to limit it to specific addresses again.
        </p>
      )}

      <datalist id="subnet-hints">
        {subnetHints.map((s) => <option key={s} value={s} />)}
      </datalist>

      {loaded && rules.length === 0 && (
        <p className="prose">
          Everything on {nodeName}&rsquo;s network. Add a limit below if the device should only
          reach one machine — a printer or a NAS, say (write a single machine as
          <code>192.168.1.50/32</code>). Entering{" "}
          <code>{CATCH_ALL}</code> instead turns on the switch above.
        </p>
      )}

      {rules.length > 0 && (
        <ul className={"rulelist" + (exit ? " off" : "")} role="list">
          <li className="rulehead" aria-hidden="true">
            <span>Address or range</span>
            <span>Traffic</span>
            <span>Ports</span>
            <span />
          </li>
          {rules.map((r, i) => (
            <li key={i} className="rulerow">
              <input
                type="text"
                list="subnet-hints"
                placeholder="192.168.1.50/32"
                aria-label={`Limit ${i + 1}: address or range`}
                value={r.dest}
                disabled={exit}
                onChange={(e) => update(i, { dest: e.target.value })}
              />
              <select
                value={r.proto}
                disabled={exit}
                aria-label={`Limit ${i + 1}: traffic type`}
                onChange={(e) => update(i, { proto: e.target.value as Rule["proto"] })}
              >
                <option value="any">Any</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              <span className="portpair">
                <input
                  type="text"
                  inputMode="numeric"
                  placeholder="any"
                  aria-label={`Limit ${i + 1}: first port`}
                  disabled={exit}
                  value={r.port_from || ""}
                  onChange={(e) => update(i, { port_from: parseInt(e.target.value) || 0 })}
                />
                <span aria-hidden="true">–</span>
                <input
                  type="text"
                  inputMode="numeric"
                  placeholder="any"
                  aria-label={`Limit ${i + 1}: last port`}
                  disabled={exit}
                  value={r.port_to || ""}
                  onChange={(e) => update(i, { port_to: parseInt(e.target.value) || 0 })}
                />
              </span>
              <button className="danger icon" disabled={exit} onClick={() => removeRule(i)} aria-label={`Remove limit ${i + 1}`}>
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}

      <div className="stack">
        <button className="ghost" onClick={addRule} disabled={exit}>Add a limit</button>
      </div>

      <ModalFooter>
        {onRevoke && (
          <button className="danger" style={{ marginRight: "auto" }} onClick={onRevoke}>
            Remove access entirely
          </button>
        )}
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="btn" onClick={save} disabled={busy}>
          {busy ? "Saving…" : "Save"}
        </button>
      </ModalFooter>
    </Modal>
  );
}
