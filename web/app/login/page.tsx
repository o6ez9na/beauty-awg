"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "../lib/api";

export default function LoginPage() {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      await api.login(username, password);
      router.push("/");
    } catch (e) {
      setErr(e instanceof ApiError ? "Invalid credentials" : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <div className="brand" style={{ marginBottom: 6, justifyContent: "center" }}>
          <img src="/logo.svg" alt="" className="brand-logo" style={{ width: 40, height: 40 }} />
          <span>6ers3<b style={{ color: "var(--signal)" }}>rk</b></span>
        </div>
        <p className="eyebrow">Sign in to the mesh</p>
        <div className="field" style={{ marginBottom: 12 }}>
          <label>Username</label>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
          />
        </div>
        <div className="field" style={{ marginBottom: 16 }}>
          <label>Password</label>
          <input
            type="text"
            style={{ WebkitTextSecurity: "disc" } as React.CSSProperties}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {err && <div className="error">{err}</div>}
        <button type="submit" className="btn" disabled={busy} style={{ width: "100%" }}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
