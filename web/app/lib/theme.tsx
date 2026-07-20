"use client";

import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useState } from "react";

// Theme choice lives here rather than in CSS alone because two things outside
// the stylesheet need the resolved value: React Flow's own `colorMode`, and the
// edge/background colors the graph sets inline.

export type ThemeChoice = "system" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

export const THEME_KEY = "6ers3rk-theme";

function readStored(): ThemeChoice {
  if (typeof window === "undefined") return "system";
  try {
    const v = window.localStorage.getItem(THEME_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch { /* private mode */ }
  return "system";
}

function systemPrefersDark(): boolean {
  if (typeof window === "undefined") return true;
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

const useIsomorphicLayoutEffect = typeof window !== "undefined" ? useLayoutEffect : useEffect;

const Ctx = createContext<{
  choice: ThemeChoice;
  resolved: ResolvedTheme;
  mounted: boolean;
  setChoice: (c: ThemeChoice) => void;
}>({ choice: "system", resolved: "dark", mounted: false, setChoice: () => {} });

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // Lazily initialised from storage so the first client render already matches
  // what the inline script in <head> painted — no flash of the wrong theme.
  const [choice, setChoiceState] = useState<ThemeChoice>(readStored);
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => setPrefersDark(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const resolved: ResolvedTheme = choice === "system" ? (prefersDark ? "dark" : "light") : choice;

  // Layout effect, not a passive one: the toggle wraps the change in a view
  // transition and needs the attribute applied inside that synchronous flush,
  // otherwise the transition captures the old palette on both sides.
  useIsomorphicLayoutEffect(() => {
    document.documentElement.setAttribute("data-theme", resolved);
  }, [resolved]);

  const setChoice = useCallback((c: ThemeChoice) => {
    setChoiceState(c);
    try { window.localStorage.setItem(THEME_KEY, c); } catch { /* private mode */ }
  }, []);

  return <Ctx.Provider value={{ choice, resolved, mounted, setChoice }}>{children}</Ctx.Provider>;
}

export const useTheme = () => useContext(Ctx);

// Runs before first paint, so the page never renders in the wrong palette.
// Kept in sync with readStored() above.
export const THEME_BOOT_SCRIPT = `(function(){try{var c=localStorage.getItem('${THEME_KEY}');var d=c==='dark'||((!c||c==='system')&&matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.setAttribute('data-theme',d?'dark':'light');}catch(e){document.documentElement.setAttribute('data-theme','dark');}})();`;
