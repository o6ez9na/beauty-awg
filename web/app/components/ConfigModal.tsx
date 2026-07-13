"use client";

import { useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../lib/api";

// Shows a rendered .conf as QR (for phone import) + copy + download.
export default function ConfigModal({
  title,
  url,
  filename,
  onClose,
}: {
  title: string;
  url: string;
  filename: string;
  onClose: () => void;
}) {
  const [text, setText] = useState<string>("");
  const [err, setErr] = useState("");

  useEffect(() => {
    api.fetchText(url).then(setText).catch((e) => setErr(String(e)));
  }, [url]);

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>
        {err && <div className="error">{err}</div>}
        {text ? (
          <>
            <div style={{ background: "white", padding: 12, borderRadius: 8, display: "inline-block" }}>
              <QRCodeSVG value={text} size={220} />
            </div>
            <div style={{ display: "flex", gap: 8, marginTop: 16, justifyContent: "center" }}>
              <button onClick={download}>Download .conf</button>
              <button className="ghost" onClick={() => navigator.clipboard.writeText(text)}>
                Copy
              </button>
              <button className="ghost" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        ) : (
          !err && <div className="mono">loading…</div>
        )}
      </div>
    </div>
  );
}
