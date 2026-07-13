import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "beautifulwg — AmneziaWG panel",
  description: "Manage AmneziaWG users, nodes and access",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
