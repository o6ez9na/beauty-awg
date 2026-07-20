"use client";

import { useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../lib/api";
import { humanError } from "../lib/errors";

type View = "qr" | "conf" | "link";

// Everything needed to get a config onto someone's device, in the order a
// normal person will reach for it: scan the code, or copy the link, or (last)
// deal with the raw file. Shared by the add-device wizard and the device
// window so both stay identical.
export default function SharePanel({ clientId, clientName }: { clientId: string; clientName: string }) {
  const [text, setText] = useState("");
  const [vpn, setVpn] = useState("");
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState("");
  const [view, setView] = useState<View>("qr");

  const confUrl = api.clientConfigUrl(clientId);
  const vpnUrl = api.clientVPNLinkUrl(clientId);

  useEffect(() => {
    let alive = true;
    api.fetchText(confUrl).then((t) => alive && setText(t)).catch((e) => alive && setErr(humanError(e)));
    api.fetchText(vpnUrl).then((t) => alive && setVpn(t)).catch(() => {});
    return () => { alive = false; };
  }, [confUrl, vpnUrl]);

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${clientName}.conf`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function copy(value: string, label: string) {
    // navigator.clipboard needs a secure context; the panel is often on plain
    // HTTP, so fall back to the old execCommand path.
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
    setTimeout(() => setCopied(""), 1800);
  }

  const loading = !text && !vpn && !err;

  return (
    <div className="share">
      {err && <div className="alert alert-error" role="alert">{err}</div>}

      <div className="seg" role="tablist" aria-label="How to share this device">
        <button className="seg-btn" role="tab" aria-selected={view === "qr"} onClick={() => setView("qr")}>
          Scan a code
        </button>
        <button className="seg-btn" role="tab" aria-selected={view === "link"} onClick={() => setView("link")}>
          Copy a link
        </button>
        <button className="seg-btn" role="tab" aria-selected={view === "conf"} onClick={() => setView("conf")}>
          Config file
        </button>
      </div>

      {loading && <div className="share-loading" aria-busy="true">Preparing the config…</div>}

      {!loading && view === "qr" && (
        <div className="share-qr">
          {(vpn || text) && (
            <>
              <div className="qr-frame">
                <QRCodeSVG value={vpn || text} size={196} />
              </div>
              <p className="hint">
                Open the AmneziaVPN app on the device, choose to add a connection, and scan this.
              </p>
            </>
          )}
        </div>
      )}

      {!loading && view === "link" && (
        <div className="share-block">
          <p className="hint">
            Send this to the device however you like. Opening it on a phone with AmneziaVPN
            installed adds the connection automatically.
          </p>
          <textarea className="codebox short" value={vpn || text} readOnly spellCheck={false} aria-label="Connection link" />
          <div className="row">
            <button className="btn" onClick={() => copy(vpn || text, "link")} disabled={!vpn && !text}>
              {copied === "link" ? "Copied" : "Copy link"}
            </button>
          </div>
        </div>
      )}

      {!loading && view === "conf" && (
        <div className="share-block">
          <p className="hint">
            For apps that want a file. You can edit it here before copying — changes are not
            saved on the server.
          </p>
          <textarea
            className="codebox"
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            aria-label="Configuration file contents"
          />
          <div className="row">
            <button className="ghost" onClick={() => copy(text, "conf")}>
              {copied === "conf" ? "Copied" : "Copy text"}
            </button>
            <button className="ghost" onClick={download}>Download file</button>
          </div>
        </div>
      )}

      {copied === "fail" && (
        <p className="hint hint-warn" role="alert">Couldn&rsquo;t copy automatically — select the text and copy it manually.</p>
      )}
    </div>
  );
}
