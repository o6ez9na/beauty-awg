"use client";

import { useState } from "react";
import { api, Node } from "../lib/api";
import { toast } from "../lib/toast";

// DNS forwarding for one location, tucked behind a disclosure: it only matters
// if you want to type names like nas.home instead of addresses, and getting it
// wrong is invisible until something stops resolving. Saved on blur, and only
// when a value actually changed.
export default function LocationSettings({
  node,
  onChange,
}: {
  node: Node;
  onChange: (fn: () => Promise<void>) => void;
}) {
  const [dns, setDns] = useState(node.dns);
  const [domains, setDomains] = useState(node.domains.join(", "));

  function save() {
    const list = domains.split(",").map((s) => s.trim()).filter(Boolean);
    if (dns === node.dns && list.join(",") === node.domains.join(",")) return;
    onChange(() => api.updateNode(node.id, dns, list));
    toast(`${node.name} changed — share the configs again for devices that use it`, "warn");
  }

  return (
    <details className="advanced">
      <summary>Name lookup</summary>
      <p className="hint">
        Optional. Lets devices reach machines here by name instead of by address.
      </p>
      <div className="field">
        <label htmlFor={`dns-${node.id}`}>Router or DNS address at this location</label>
        <input
          id={`dns-${node.id}`}
          type="text"
          value={dns}
          placeholder="192.168.1.1"
          onChange={(e) => setDns(e.target.value)}
          onBlur={save}
        />
      </div>
      <div className="field">
        <label htmlFor={`dom-${node.id}`}>Names it answers for</label>
        <input
          id={`dom-${node.id}`}
          type="text"
          value={domains}
          placeholder="home.lan"
          onChange={(e) => setDomains(e.target.value)}
          onBlur={save}
        />
      </div>
    </details>
  );
}
