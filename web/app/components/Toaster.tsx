"use client";

import { useEffect, useState } from "react";
import { subscribe, Toast } from "../lib/toast";

// Mounted once in the shell; renders the toast stack bottom-right.
export default function Toaster() {
  const [ts, setTs] = useState<Toast[]>([]);
  useEffect(() => subscribe(setTs), []);

  return (
    <div className="toaster">
      {ts.map((t) => (
        <div key={t.id} className={"toast toast-" + t.kind}>
          <span className="toast-dot" />
          {t.msg}
        </div>
      ))}
    </div>
  );
}
