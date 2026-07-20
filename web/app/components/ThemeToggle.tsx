"use client";

import { ThemeChoice, useTheme } from "../lib/theme";

const ORDER: ThemeChoice[] = ["system", "light", "dark"];
const LABEL: Record<ThemeChoice, string> = { system: "Auto", light: "Light", dark: "Dark" };

// Cycles Auto → Light → Dark. "Auto" is the default and follows the OS, so
// someone who never touches this still gets the theme they already asked their
// machine for.
export default function ThemeToggle() {
  const { choice, mounted, setChoice } = useTheme();
  const next = ORDER[(ORDER.indexOf(choice) + 1) % ORDER.length];

  return (
    <button
      className="ghost theme-btn"
      onClick={() => setChoice(next)}
      // Until the client has read localStorage the label would be a guess, and
      // rendering the wrong one would mismatch the server markup.
      aria-label={mounted ? `Theme: ${LABEL[choice]}. Switch to ${LABEL[next]}` : "Theme"}
      title={mounted ? `Theme: ${LABEL[choice]} — click for ${LABEL[next]}` : "Theme"}
      suppressHydrationWarning
    >
      {mounted ? LABEL[choice] : "Theme"}
    </button>
  );
}
