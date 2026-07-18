"use client";

import { useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { api, Client } from "../lib/api";
import ConfirmModal from "./ConfirmModal";
import ColorPickerModal from "./ColorPickerModal";

type View = "qr" | "conf" | "link";

// Full client window: editable name + a segmented view over the QR, the raw
// AmneziaWG .conf (view / edit / copy / download) and the native vpn:// link.
// Reused by the clients list and the access graph. Color lives here (not the
// graph card) so the graph itself stays free of controls.
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
  const [text, setText] = useState("");
  const [vpn, setVpn] = useState("");
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState("");
  const [name, setName] = useState(client.name);
  const [view, setView] = useState<View>("qr");
  const [confirming, setConfirming] = useState(false);
  const [pickingColor, setPickingColor] = useState(false);

  const confUrl = api.clientConfigUrl(client.id);
  const vpnUrl = api.clientVPNLinkUrl(client.id);

  useEffect(() => {
    api.fetchText(confUrl).then(setText).catch((e) => setErr(String(e)));
    api.fetchText(vpnUrl).then(setVpn).catch(() => {});
  }, [confUrl, vpnUrl]);

  function saveName() {
    const n = name.trim();
    if (n && n !== client.name) onRename(n);
    else setName(client.name);
  }

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${client.name}.conf`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function copy(value: string, label: string) {
    // navigator.clipboard needs a secure context; fall back to execCommand on HTTP.
    let ok = false;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value);
        ok = true;
      }
    } catch { ok = false; }
    if (!ok) {
      const ta = document.createElement("textarea");
      ta.value = value;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      try { ok = document.execCommand("copy"); } catch { ok = false; }
      document.body.removeChild(ta);
    }
    setCopied(ok ? label : "fail");
    setTimeout(() => setCopied(""), 1500);
  }

  const taStyle: React.CSSProperties = {
    width: "100%", minHeight: 180, resize: "vertical",
    background: "var(--ink)", border: "1px solid var(--line)", color: "var(--text)",
    borderRadius: 8, padding: 11, fontFamily: "var(--mono)", fontSize: 12, lineHeight: 1.5,
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ width: 460, textAlign: "left" }} onClick={(e) => e.stopPropagation()}>
        <button className="modal-x" onClick={onClose} aria-label="Close">✕</button>

        <div className="row" style={{ gap: 9, paddingRight: 40, marginBottom: 4, flexWrap: "nowrap" }}>
          <span className={"dot " + (client.online ? "live" : "")} title={client.online ? "online" : "offline"} />
          <input
            type="text"
            value={name}
            aria-label="Client name"
            onChange={(e) => setName(e.target.value)}
            onBlur={saveName}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
            style={{ flex: 1, fontWeight: 650, fontFamily: "var(--sans)", fontSize: 15 }}
          />
        </div>
        <div className="mono" style={{ fontSize: 12, color: "var(--muted)", marginBottom: 14 }}>
          {client.address} · {client.enabled ? "enabled" : "disabled"}
        </div>

        {err && <div className="error">{err}</div>}

        <div className="seg" role="tablist" style={{ marginBottom: 14 }}>
          <button className="seg-btn" role="tab" aria-selected={view === "qr"} onClick={() => setView("qr")}>QR</button>
          <button className="seg-btn" role="tab" aria-selected={view === "conf"} onClick={() => setView("conf")}>Config</button>
          <button className="seg-btn" role="tab" aria-selected={view === "link"} onClick={() => setView("link")}>VPN link</button>
        </div>

        {view === "qr" && (
          <div style={{ textAlign: "center" }}>
            {(vpn || text) ? (
              <>
                <div style={{ background: "white", padding: 12, borderRadius: 8, display: "inline-block" }}>
                  <QRCodeSVG value={vpn || text} size={200} />
                </div>
                <div className="mono" style={{ fontSize: 11, marginTop: 8, color: "var(--muted)" }}>
                  {vpn ? "QR = AmneziaVPN import (vpn://)" : "QR = .conf"}
                </div>
              </>
            ) : (!err && <div className="mono">loading…</div>)}
          </div>
        )}

        {view === "conf" && (
          <>
            <textarea value={text} onChange={(e) => setText(e.target.value)} style={taStyle} spellCheck={false} />
            <div className="row" style={{ marginTop: 10 }}>
              <button className="ghost" onClick={() => copy(text, "conf")}>
                {copied === "conf" ? "Copied!" : "Copy"}
              </button>
              <button className="ghost" onClick={download}>Download .conf</button>
            </div>
            <div className="mono" style={{ fontSize: 10.5, color: "var(--faint)", marginTop: 6 }}>
              edits apply to copy / download only — not saved on the server
            </div>
          </>
        )}

        {view === "link" && (
          <>
            <textarea value={vpn} readOnly style={{ ...taStyle, minHeight: 120 }} spellCheck={false} />
            <div className="row" style={{ marginTop: 10 }}>
              <button className="btn" onClick={() => copy(vpn, "link")} disabled={!vpn}>
                {copied === "link" ? "Copied!" : "Copy vpn:// link"}
              </button>
            </div>
          </>
        )}

        <div className="row" style={{ marginTop: 16, justifyContent: "flex-end", borderTop: "1px solid var(--line)", paddingTop: 14 }}>
          <button className="ghost" onClick={() => setPickingColor(true)}>Color</button>
          <button className="danger" onClick={() => setConfirming(true)}>Delete client</button>
        </div>
      </div>

      {confirming && (
        <ConfirmModal
          title={`Delete ${client.name}?`}
          body="Removes the client and revokes its access. This cannot be undone."
          onConfirm={onDelete}
          onClose={() => setConfirming(false)}
        />
      )}

      {pickingColor && (
        <ColorPickerModal
          title={`Color — ${client.name}`}
          current={client.color}
          seed={client.address || client.id}
          onSave={onColor}
          onClose={() => setPickingColor(false)}
        />
      )}
    </div>
  );
}
