"use client";

import { Handle, NodeResizer, Position, type NodeProps } from "@xyflow/react";

import { GROUP_MIN_H, GROUP_MIN_W } from "../lib/groups";

export type GroupCardData = {
  label: string;
  count: number;
  /** Floor for manual resizing: the height this many members need. */
  minHeight?: number;
  /** "#rrggbb", or "" for the default frame */
  color?: string;
  sel?: boolean;
  dim?: boolean;
  candidate?: boolean;
  muted?: boolean;
  dragging?: boolean;
  onGear?: () => void;
  onResize?: (w: number, h: number) => void;
};

// The group is a real React Flow parent: its devices are child nodes living
// inside it, not a list drawn on the card. That is what makes them snap to the
// frame and move with it.
//
// The node type is deliberately NOT called "group" — React Flow ships styling
// for that built-in type name (padding, border, a 150px width and a background),
// which renders as a stray box behind any custom component using it.
export default function GroupNode({ data }: NodeProps) {
  const d = data as GroupCardData;
  return (
    <div
      className={
        "ggroup" + (d.sel ? " sel" : "") + (d.dim ? " dim" : "") +
        (d.candidate ? " candidate" : "") + (d.muted ? " muted" : "") +
        (d.dragging ? " dragging" : "")
      }
      style={d.color ? ({ "--group-accent": d.color } as React.CSSProperties) : undefined}
    >
      {/* Tied to the panel's own selection (a click on the card), not React
          Flow's internal `selected`, so the handles appear on exactly the same
          gesture that highlights everything else. */}
      <NodeResizer
        minWidth={GROUP_MIN_W}
        minHeight={d.minHeight ?? GROUP_MIN_H}
        isVisible={!!d.sel}
        onResizeEnd={(_, p) => d.onResize?.(Math.round(p.width), Math.round(p.height))}
      />

      <div className="ggroup-head">
        <span className="ggroup-badge">{d.count}</span>
        <span className="ggroup-name">{d.label}</span>
        {d.onGear && (
          <button
            className="ggroup-gear nodrag nopan"
            aria-label={`Group settings for ${d.label}`}
            title={`Group settings for ${d.label}`}
            onClick={(e) => { e.stopPropagation(); d.onGear?.(); }}
            onMouseDown={(e) => e.stopPropagation()}
          >
            ⚙
          </button>
        )}
      </div>

      {d.count === 0 && <p className="ggroup-empty">Drag devices in here.</p>}

      <Handle type="source" position={Position.Right} />
    </div>
  );
}
