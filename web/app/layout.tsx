import type { Metadata } from "next";
import "./globals.css";
import { ThemeProvider, THEME_BOOT_SCRIPT } from "./lib/theme";

export const metadata: Metadata = {
  title: "6ers3rk — AmneziaWG panel",
  description: "Manage AmneziaWG users, nodes and access",
  icons: {
    icon: [
      { url: "/favicon.svg", type: "image/svg+xml" },
      { url: "/favicon-32x32.png", type: "image/png", sizes: "32x32" },
      { url: "/favicon.ico", sizes: "any" },
    ],
    apple: [{ url: "/apple-touch-icon.png", sizes: "180x180" }],
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // The boot script sets data-theme before hydration, so the server markup
    // deliberately differs from what the browser has by then.
    <html lang="en" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOT_SCRIPT }} />
      </head>
      <body>
        <ThemeProvider>{children}</ThemeProvider>
      </body>
    </html>
  );
}
