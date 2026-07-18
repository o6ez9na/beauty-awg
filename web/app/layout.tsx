import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "6ers3rk — AmneziaWG panel",
  description: "Manage AmneziaWG users, nodes and access",
  icons: {
    icon: [{ url: "/favicon.svg", type: "image/svg+xml" }],
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
