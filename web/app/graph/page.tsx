"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// The access graph now lives on the main dashboard; keep this path as a redirect.
export default function GraphRedirect() {
  const router = useRouter();
  useEffect(() => { router.replace("/"); }, [router]);
  return null;
}
