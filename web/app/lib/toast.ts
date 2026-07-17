// Tiny pub/sub toast bus. Any module can fire a toast without prop-drilling; the
// <Toaster/> mounted once in the shell renders them. Used to warn the admin when
// an action changed a client's config and it must be re-shared.

export type Toast = { id: number; msg: string; kind: "info" | "warn" };

let seq = 0;
let toasts: Toast[] = [];
const listeners = new Set<(t: Toast[]) => void>();

function emit() {
  for (const l of listeners) l(toasts);
}

export function toast(msg: string, kind: Toast["kind"] = "info") {
  const t: Toast = { id: ++seq, msg, kind };
  toasts = [...toasts, t];
  emit();
  setTimeout(() => {
    toasts = toasts.filter((x) => x.id !== t.id);
    emit();
  }, 6000);
}

// Config-changed helper: one consistent message so the admin knows to re-share.
export function configChanged(who: string) {
  toast(`${who}: config changed — re-share it`, "warn");
}

export function subscribe(l: (t: Toast[]) => void) {
  listeners.add(l);
  l(toasts);
  return () => { listeners.delete(l); };
}
