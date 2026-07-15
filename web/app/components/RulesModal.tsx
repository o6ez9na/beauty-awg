"use client";

import { useEffect, useState } from "react";
import { api, Rule } from "../lib/api";

// Editor for a grant's access level: destination subnets/hosts + optional ports.
// No rules = full access to all the node's subnets.
export default function RulesModal({
  clientId,
  nodeId,
  clientName,
  nodeName,
  subnetHints,
  onClose,
}: {
  clientId: string;
  nodeId: string;
  clientName: string;
  nodeName: string;
  subnetHints: string[];
  onClose: () => void;
}) {
  const [rules, setRules] = useState<Rule[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    api
      .getGrantRules(clientId, nodeId)
      .then((r) => setRules(r || []))
      .catch((e) => setErr(String(e)))
      .finally(() => setLoaded(true));
  }, [clientId, nodeId]);

  function update(i: number, patch: Partial<Rule>) {
    setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  }
  function addRule() {
    setRules((rs) => [
      ...rs,
      { dest: subnetHints[0] || "", proto: "any", port_from: 0, port_to: 0 },
    ]);
  }
  function removeRule(i: number) {
    setRules((rs) => rs.filter((_, j) => j !== i));
  }

  async function save() {
    setBusy(true);
    setErr("");
    try {
      await api.setGrantRules(clientId, nodeId, rules);
      onClose();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal"
        style={{ width: 560, textAlign: "left" }}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 style={{ marginTop: 0 }}>
          Access level:{" "}
          <span style={{ color: "var(--accent)" }}>{clientName}</span> →{" "}
          <span style={{ color: "var(--ok)" }}>{nodeName}</span>
        </h2>
        <p className="mono" style={{ marginTop: -6 }}>
          No rules = full access to all node subnets.
        </p>
        {err && <div className="error">{err}</div>}

        <datalist id="subnet-hints">
          {subnetHints.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>

        {loaded && rules.length === 0 && (
          <div className="mono" style={{ margin: "10px 0" }}>
            full access (no restrictions)
          </div>
        )}

        {rules.map((r, i) => (
          <div
            key={i}
            className="row"
            style={{ marginBottom: 8, gap: 6, flexWrap: "nowrap" }}
          >
            <input
              type="text"
              list="subnet-hints"
              placeholder="192.168.1.0/24"
              value={r.dest}
              onChange={(e) => update(i, { dest: e.target.value })}
              style={{ flex: 2, width: "100vw" }}
            />
            <select
              value={r.proto}
              onChange={(e) =>
                update(i, { proto: e.target.value as Rule["proto"] })
              }
              style={{
                background: "var(--ink)",
                color: "var(--text)",
                border: "1px solid var(--line)",
                borderRadius: 7,
                padding: "8px",
              }}
            >
              <option value="any">any</option>
              <option value="tcp">tcp</option>
              <option value="udp">udp</option>
            </select>
            <input
              type="text"
              placeholder="port"
              value={r.port_from || ""}
              onChange={(e) =>
                update(i, { port_from: parseInt(e.target.value) || 0 })
              }
              style={{ width: 70 }}
            />
            <span className="mono">–</span>
            <input
              type="text"
              placeholder="to"
              value={r.port_to || ""}
              onChange={(e) =>
                update(i, { port_to: parseInt(e.target.value) || 0 })
              }
              style={{ width: 70 }}
            />
            <button className="danger" onClick={() => removeRule(i)}>
              ✕
            </button>
          </div>
        ))}

        <div className="row" style={{ marginTop: 12 }}>
          <button className="ghost" onClick={addRule}>
            + Add rule
          </button>
          <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
            <button className="ghost" onClick={onClose}>
              Cancel
            </button>
            <button className="btn" onClick={save} disabled={busy}>
              {busy ? "Saving…" : "Save"}
            </button>
          </span>
        </div>
      </div>
    </div>
  );
}
