"use client";

import { useEffect, useId, useRef } from "react";

// Shared dialog shell. Every modal in the panel goes through this so they all
// behave the same: Escape closes, focus moves in and is trapped, focus returns
// where it came from, and the page behind doesn't scroll.
//
// Modals nest (a device's Share window opens Delete-confirm on top), so only
// the topmost one may react to Escape — hence the module-level stack.

const stack: symbol[] = [];

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export type ModalSize = "sm" | "md" | "lg";

export default function Modal({
  title,
  subtitle,
  size = "sm",
  onClose,
  children,
}: {
  title: string;
  subtitle?: React.ReactNode;
  size?: ModalSize;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const token = useRef(Symbol("modal"));

  // Held in a ref so the setup effect below can run ONCE, on mount. Callers pass
  // onClose as an inline arrow, so it is a new function on every render of the
  // page — and the page re-renders every few seconds as polling refreshes the
  // device list. An effect depending on it would tear down and set up again on
  // each of those, pulling focus back to the first field mid-typing.
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    const me = token.current;
    stack.push(me);
    const restoreTo = document.activeElement as HTMLElement | null;

    // Focus the first real control, but skip the close button — landing on "✕"
    // reads as "you are about to dismiss this" rather than "here is the thing".
    const panel = panelRef.current;
    const candidates = panel ? Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)) : [];
    const first = candidates.find((el) => !el.hasAttribute("data-modal-close")) ?? candidates[0];
    (first ?? panel)?.focus();

    function onKeyDown(e: KeyboardEvent) {
      if (stack[stack.length - 1] !== me) return;

      if (e.key === "Escape") {
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab" || !panelRef.current) return;

      const items = Array.from(panelRef.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) => el.offsetParent !== null
      );
      if (items.length === 0) return;
      const firstItem = items[0];
      const lastItem = items[items.length - 1];
      const active = document.activeElement;

      if (e.shiftKey && (active === firstItem || !panelRef.current.contains(active))) {
        e.preventDefault();
        lastItem.focus();
      } else if (!e.shiftKey && active === lastItem) {
        e.preventDefault();
        firstItem.focus();
      }
    }

    document.addEventListener("keydown", onKeyDown, true);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      const i = stack.indexOf(me);
      if (i !== -1) stack.splice(i, 1);
      if (stack.length === 0) document.body.style.overflow = prevOverflow;
      restoreTo?.focus?.();
    };
    // Mount-only: see onCloseRef above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={panelRef}
        className={"modal modal-" + size}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className="modal-head">
          <h2 id={titleId}>{title}</h2>
          {subtitle && <p className="modal-sub">{subtitle}</p>}
        </div>
        <button className="modal-x" data-modal-close onClick={onClose} aria-label="Close">
          ✕
        </button>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}

/** Right-aligned action row pinned under the modal body. */
export function ModalFooter({ children }: { children: React.ReactNode }) {
  return <div className="modal-foot">{children}</div>;
}
