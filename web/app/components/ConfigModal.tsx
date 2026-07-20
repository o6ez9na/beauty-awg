"use client";

import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { humanError } from "../lib/errors";
import Modal, { ModalFooter } from "./Modal";

// The setup file for a location. Unlike a device config this is never scanned
// from a phone — it gets copied onto the machine at that location — so it
// leads with the file and the two commands that install it.
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
  const [text, setText] = useState("");
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    let alive = true;
    api.fetchText(url).then((t) => alive && setText(t)).catch((e) => alive && setErr(humanError(e)));
    return () => { alive = false; };
  }, [url]);

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function copy(value: string, label: string) {
    // navigator.clipboard needs a secure context; fall back on plain HTTP.
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

  return (
    <Modal
      title={`Setup file for ${title}`}
      subtitle="Put this on the machine at that location."
      size="md"
      onClose={onClose}
    >
      {err && <div className="alert alert-error" role="alert">{err}</div>}

      <p className="prose">
        If you installed that machine with the node installer, it updates itself and you
        don&rsquo;t need this. Use it only for a machine you set up by hand: save the file as{" "}
        <code>/etc/amnezia/amneziawg/awg0.conf</code>, then run{" "}
        <code>awg-quick up awg0</code>.
      </p>

      {text ? (
        <>
          <textarea className="codebox" value={text} readOnly spellCheck={false} aria-label="Setup file contents" />
          <div className="row">
            <button className="btn" onClick={download}>Download file</button>
            <button className="ghost" onClick={() => copy(text, "conf")}>
              {copied === "conf" ? "Copied" : "Copy text"}
            </button>
          </div>
          {copied === "fail" && (
            <p className="hint hint-warn" role="alert">
              Couldn&rsquo;t copy automatically — select the text and copy it manually.
            </p>
          )}
        </>
      ) : (
        !err && <div className="share-loading" aria-busy="true">Loading…</div>
      )}

      <ModalFooter>
        <button className="ghost" onClick={onClose}>Close</button>
      </ModalFooter>
    </Modal>
  );
}
