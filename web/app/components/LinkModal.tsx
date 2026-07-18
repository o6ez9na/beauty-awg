"use client";

// Editor for a directed site-to-site link (node -> node). Shows the direction,
// lets you add/remove the reverse direction, or remove the link entirely.
export default function LinkModal({
  srcName,
  dstName,
  hasReverse,
  onAddReverse,
  onRemoveReverse,
  onRemove,
  onClose,
}: {
  srcName: string;
  dstName: string;
  hasReverse: boolean;
  onAddReverse: () => void;
  onRemoveReverse: () => void;
  onRemove: () => void;
  onClose: () => void;
}) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ textAlign: "left" }} onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>Site-to-site link</h2>
        <p className="mono" style={{ marginTop: -6 }}>
          <b>{srcName}</b>&rsquo;s LAN can reach <b>{dstName}</b>&rsquo;s LAN.
          {hasReverse
            ? " The reverse direction is also allowed."
            : " Traffic can only be initiated in this direction (replies return automatically)."}
        </p>
        <div className="row" style={{ marginTop: 16, justifyContent: "space-between" }}>
          {hasReverse ? (
            <button className="ghost" onClick={() => { onRemoveReverse(); onClose(); }}>
              Remove reverse ({dstName} → {srcName})
            </button>
          ) : (
            <button className="ghost" onClick={() => { onAddReverse(); onClose(); }}>
              Also allow {dstName} → {srcName}
            </button>
          )}
          <div className="row" style={{ gap: 8 }}>
            <button className="ghost" onClick={onClose}>Close</button>
            <button className="danger" onClick={() => { onRemove(); onClose(); }}>Remove link</button>
          </div>
        </div>
      </div>
    </div>
  );
}
