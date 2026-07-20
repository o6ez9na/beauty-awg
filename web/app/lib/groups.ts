// Groups bundle devices that should have the same access, so the map shows one
// line per destination instead of one per device.
//
// They live in the graph-layout blob rather than in their own table: the layout
// endpoint stores whatever JSON it is given (the handler only checks that it
// parses), so groups need no migration and no backend release. The trade-off is
// that they are an admin-view convenience only — the backend still knows just
// devices and grants, and a group's access is the grants of its members.

import { XYPosition } from "@xyflow/react";

// No x/y here: a group is positioned through the same positions map as every
// other card (key "g:<id>"), so dragging one is persisted by the same code path
// and there is only ever one source of truth for where a card sits. Size does
// live here — it has no equivalent in that map.
export type Group = {
  id: string;
  name: string;
  w?: number;
  h?: number;
  /** "#rrggbb", or "" / absent for the default frame colour */
  color?: string;
  /** client ids */
  members: string[];
};

export const GROUP_MIN_W = 200;
export const GROUP_MIN_H = 130;
/** Default frame: roomy enough for three stacked cards before resizing. */
export const GROUP_DEF_W = 250;
export const GROUP_DEF_H = 230;
/** Free space under the header before child cards start. */
export const GROUP_PAD_TOP = 40;
export const GROUP_PAD = 12;
/** Vertical pitch of the member stack. */
export const GROUP_ROW = 56;

/** Where the next member sits: a tidy stack rather than wherever the card was
 *  dropped, which is what makes them line up instead of piling on each other. */
export function memberSeat(index: number) {
  return { x: GROUP_PAD, y: GROUP_PAD_TOP + index * GROUP_ROW };
}

/** Reserved key inside the layout object. Node positions are keyed by
 *  "c:<uuid>"/"n:<uuid>", so a "__"-prefixed key can never collide. */
export const GROUPS_KEY = "__groups";

export type Layout = {
  positions: Record<string, XYPosition>;
  groups: Group[];
};

function isPosition(v: unknown): v is XYPosition {
  return !!v && typeof v === "object" && typeof (v as XYPosition).x === "number" && typeof (v as XYPosition).y === "number";
}

function isGroup(v: unknown): v is Group {
  if (!v || typeof v !== "object") return false;
  const g = v as Group;
  return (
    typeof g.id === "string" &&
    typeof g.name === "string" &&
    Array.isArray(g.members) &&
    g.members.every((m) => typeof m === "string")
  );
}

/** Split the stored blob into positions and groups, ignoring anything malformed
 *  — a layout written by an older or newer build must never break the map. */
export function parseLayout(raw: unknown): Layout {
  const out: Layout = { positions: {}, groups: [] };
  if (!raw || typeof raw !== "object") return out;

  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    if (key === GROUPS_KEY) {
      if (Array.isArray(value)) out.groups = value.filter(isGroup);
      continue;
    }
    if (isPosition(value)) out.positions[key] = value;
  }
  return out;
}

export function serializeLayout(layout: Layout): Record<string, unknown> {
  const blob: Record<string, unknown> = { ...layout.positions };
  // Omit the key entirely when there are no groups, so a layout that never used
  // them stays byte-identical to what older builds wrote.
  if (layout.groups.length > 0) blob[GROUPS_KEY] = layout.groups;
  return blob;
}

export const gid = (id: string) => `g:${id}`;
export const isGroupId = (rfId: string) => rfId.startsWith("g:");

/** The group a client belongs to, if any. */
export function groupOf(groups: Group[], clientId: string): Group | undefined {
  return groups.find((g) => g.members.includes(clientId));
}

/** Every client id that sits in some group. */
export function groupedClientIds(groups: Group[]): Set<string> {
  return new Set(groups.flatMap((g) => g.members));
}

/** What a group grants: the union of what its members are granted. Members are
 *  kept in sync on join and on every group-level change, so in a settled state
 *  the union and the intersection are the same set. Using the union means a
 *  half-applied change still shows the line it was trying to create. */
export function groupGrants(group: Group, clientGrants: Map<string, string[]>): string[] {
  const out = new Set<string>();
  for (const m of group.members) {
    for (const nodeId of clientGrants.get(m) ?? []) out.add(nodeId);
  }
  return [...out];
}

/** What has to change for `clientId` to match the group exactly. */
export function syncPlan(
  target: string[],
  current: string[]
): { add: string[]; remove: string[] } {
  const t = new Set(target);
  const c = new Set(current);
  return {
    add: [...t].filter((n) => !c.has(n)),
    remove: [...c].filter((n) => !t.has(n)),
  };
}

export function newGroupId(): string {
  return Math.random().toString(36).slice(2, 10);
}
