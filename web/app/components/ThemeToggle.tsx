"use client";

import { flushSync } from "react-dom";
import { ThemeChoice, useTheme } from "../lib/theme";

// Three explicit states rather than a cycling button: "Auto" is the default and
// follows the OS, and a cycler hides which of the three you are actually on —
// you had to hover and read the tooltip to find out.
const OPTIONS: { value: ThemeChoice; label: string }[] = [
  { value: "system", label: "Match my system" },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
];

function Icon({ kind }: { kind: ThemeChoice }) {
  if (kind === "light") {
    return (
      <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <circle cx="12" cy="12" r="4.4" fill="currentColor" />
        {[0, 45, 90, 135, 180, 225, 270, 315].map((a) => (
          <line
            key={a}
            x1="12" y1="2.6" x2="12" y2="5.2"
            stroke="currentColor" strokeWidth="1.9" strokeLinecap="round"
            transform={`rotate(${a} 12 12)`}
          />
        ))}
      </svg>
    );
  }
  if (kind === "dark") {
    return (
      <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <path d="M20 14.2A8.4 8.4 0 0 1 9.8 4a8.4 8.4 0 1 0 10.2 10.2Z" fill="currentColor" />
      </svg>
    );
  }
  // Auto: a disc split light/dark, which is what "follow the system" resolves to.
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="7.6" fill="none" stroke="currentColor" strokeWidth="1.9" />
      <path d="M12 4.4a7.6 7.6 0 0 1 0 15.2Z" fill="currentColor" />
    </svg>
  );
}

export default function ThemeToggle() {
  const { choice, mounted, setChoice } = useTheme();
  const index = Math.max(0, OPTIONS.findIndex((o) => o.value === choice));

  function pick(next: ThemeChoice, e: React.MouseEvent<HTMLButtonElement>) {
    if (next === choice) return;

    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    // View transitions are progressive: without support (or with motion turned
    // down) the theme just swaps, which is the correct fallback.
    const doc = document as Document & {
      startViewTransition?: (cb: () => void) => { ready: Promise<void> };
    };
    if (reduced || typeof doc.startViewTransition !== "function") {
      setChoice(next);
      return;
    }

    // Reveal the new palette as a circle growing from the button that was
    // clicked, out to whichever viewport corner is furthest away.
    const r = e.currentTarget.getBoundingClientRect();
    const x = r.left + r.width / 2;
    const y = r.top + r.height / 2;
    const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));

    const transition = doc.startViewTransition(() => {
      // flushSync so the attribute lands inside the transition's capture.
      flushSync(() => setChoice(next));
    });

    transition.ready
      .then(() => {
        document.documentElement.animate(
          { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
          {
            duration: 480,
            easing: "cubic-bezier(0.4, 0, 0.2, 1)",
            pseudoElement: "::view-transition-new(root)",
          }
        );
      })
      .catch(() => { /* transition skipped; the theme still changed */ });
  }

  return (
    <div
      className="themeswitch"
      role="radiogroup"
      aria-label="Colour theme"
      style={{ ["--idx" as string]: mounted ? index : 0 }}
    >
      <span className="themeswitch-pill" aria-hidden="true" />
      {OPTIONS.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          // Until the client has read localStorage the state would be a guess,
          // and rendering the wrong one would mismatch the server markup.
          aria-checked={mounted ? choice === o.value : undefined}
          aria-label={o.label}
          title={o.label}
          className={"themeswitch-btn" + (mounted && choice === o.value ? " on" : "")}
          onClick={(e) => pick(o.value, e)}
          suppressHydrationWarning
        >
          <Icon kind={o.value} />
        </button>
      ))}
    </div>
  );
}
