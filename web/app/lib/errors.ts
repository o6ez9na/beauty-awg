// The backend speaks in network terms ("subnets overlap", "pool exhausted").
// Those strings are correct but useless to someone who just wants to give a
// friend access to their home NAS. Map the ones a normal user can actually hit
// onto plain language plus, where possible, the next thing to do about it.

import { ApiError } from "./api";

// message is either a fixed sentence or a builder that can weave in captured
// groups from the regex (e.g. the name of the location that is already the exit),
// so the advice can point at the exact thing to change.
type Match = { test: RegExp; message: string | ((m: RegExpMatchArray) => string) };

const MATCHES: Match[] = [
  {
    test: /subnets overlap \(([^)]*)\)/i,
    message:
      "These two locations use the same local network range, so traffic can't tell them apart. Change one router's range (for example from 192.168.1.x to 192.168.5.x) and try again.",
  },
  {
    // A device routes all its traffic to one place, so it can exit through only
    // one location. The backend names the location it already exits through, so
    // the fix ("turn that one off first") is one unambiguous click. Different
    // devices/groups can still exit through different locations at the same time.
    test: /already sends all traffic through "([^"]+)"/i,
    message: (m) =>
      `This device already browses the internet through “${m[1]}”. A device can use only one internet exit at a time — turn it off on “${m[1]}” first, then switch it on here.`,
  },
  {
    test: /internet-exit hub node cannot be part of a site-to-site link/i,
    message: "The internet exit can't be linked to another location.",
  },
  {
    test: /internet-exit hub node is already a full-tunnel exit/i,
    message: "The internet exit already sends all traffic — no extra setting needed.",
  },
  { test: /a node cannot link to itself/i, message: "A location can't link to itself." },
  {
    test: /both nodes must exist and be active/i,
    message: "Both locations need to be approved and online before you can link them.",
  },
  {
    test: /both nodes must have at least one LAN subnet/i,
    message: "Both locations need a local network range set before you can link them.",
  },
  { test: /pool .* exhausted/i, message: "No addresses left. Delete an unused device to free one up." },
  { test: /node not found/i, message: "That location no longer exists. Refresh the page." },
  { test: /grant does not exist/i, message: "That access was already removed. Refresh the page." },
  { test: /name required/i, message: "Please enter a name." },
  {
    test: /name, lan_iface and at least one subnet required/i,
    message: "Please fill in the name and the local network range.",
  },
  { test: /bad subnet/i, message: "That local network range isn't valid. Use a form like 192.168.1.0/24." },
  {
    test: /bad dest/i,
    // The backend parses this as a prefix, so a bare address is rejected — the
    // suggestion has to carry the /32 or it sends people straight back here.
    message: "That destination isn't valid. Use a range like 192.168.1.0/24, or a single machine written as 192.168.1.50/32.",
  },
  { test: /invalid credentials/i, message: "Wrong username or password." },
  { test: /color must be empty or/i, message: "That color isn't valid." },
];

/** A sentence a non-technical person can act on, for any thrown value. */
export function humanError(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e);

  for (const m of MATCHES) {
    const hit = raw.match(m.test);
    if (hit) return typeof m.message === "function" ? m.message(hit) : m.message;
  }

  if (e instanceof ApiError) {
    if (e.status === 401) return "Your session expired. Please sign in again.";
    if (e.status === 403) return "You don't have permission to do that.";
    if (e.status === 404) return "That item no longer exists. Refresh the page.";
    if (e.status === 409) return "Something else changed this at the same time. Refresh and try again.";
    if (e.status >= 500) return "The server had a problem. Try again in a moment.";
  }

  // Network-level failure: fetch rejects rather than returning a status.
  if (raw.includes("Failed to fetch") || raw.includes("NetworkError")) {
    return "Can't reach the panel. Check your connection and try again.";
  }

  // Unknown backend text: show it, but strip the "ApiError:" prefix noise.
  const cleaned = raw.replace(/^\w*Error:\s*/, "").trim();
  return cleaned || "Something went wrong. Please try again.";
}
