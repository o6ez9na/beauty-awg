"use client";

import { useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../lib/api";

// Shows a config as QR + copy + download. For clients, pass vpnLinkUrl to also
// offer the native AmneziaVPN "vpn://" link (QR + Copy import into the app).
export default function ConfigModal({
  title,
  url,
  filename,
  vpnLinkUrl,
  onClose,
}: {
  title: string;
  url: string;
  filename: string;
  vpnLinkUrl?: string;
  onClose: () => void;
}) {
  const [text, setText] = useState<string>("");
  const [vpn, setVpn] = useState<string>("");
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    api.fetchText(url).then(setText).catch((e) => setErr(String(e)));
    if (vpnLinkUrl) api.fetchText(vpnLinkUrl).then(setVpn).catch(() => {});
  }, [url, vpnLinkUrl]);

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function copy(value: string, label: string) {
    // navigator.clipboard only exists in a secure context (HTTPS/localhost); the
    // panel is often served over plain HTTP, so fall back to execCommand.
    let ok = false;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value);
        ok = true;
      }
    } catch {
      ok = false;
    }
    if (!ok) {
      const ta = document.createElement("textarea");
      ta.value = value;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      try {
        ok = document.execCommand("copy");
      } catch {
        ok = false;
      }
      document.body.removeChild(ta);
    }
    setCopied(ok ? label : "fail");
    setTimeout(() => setCopied(""), 1500);
  }

  // Native AmneziaVPN link (if available) is the primary QR/import; else the .conf.
  const qrValue = vpn || text;

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>
        {err && <div className="error">{err}</div>}
        {qrValue ? (
          <>
            <div style={{ background: "white", padding: 12, borderRadius: 8, display: "inline-block" }}>
              <QRCodeSVG value={qrValue} size={220} />
            </div>
            {vpn && (
              <div className="mono" style={{ fontSize: 11, marginTop: 8, color: "var(--muted)" }}>
                QR = AmneziaVPN import (vpn://)
              </div>
            )}
            <div style={{ display: "flex", gap: 8, marginTop: 14, justifyContent: "center", flexWrap: "wrap" }}>
              {vpn && (
                <button className="btn" onClick={() => copy(vpn, "link")}>
                  {copied === "link" ? "Copied!" : "Copy vpn:// link"}
                </button>
              )}
              <button className="ghost" onClick={download}>Download .conf</button>
              <button className="ghost" onClick={() => copy(text, "conf")}>
                {copied === "conf" ? "Copied!" : "Copy .conf"}
              </button>
              <button className="ghost" onClick={onClose}>Close</button>
            </div>
          </>
        ) : (
          !err && <div className="mono">loading…</div>
        )}
      </div>
    </div>
  );
}
