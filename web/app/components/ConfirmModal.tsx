"use client";

// Small yes/no gate for destructive actions (delete a client or node).
export default function ConfirmModal({
  title,
  body,
  confirmLabel = "Delete",
  onConfirm,
  onClose,
}: {
  title: string;
  body?: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ textAlign: "left" }} onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>
        {body && <p className="mono" style={{ marginTop: -6 }}>{body}</p>}
        <div className="row" style={{ marginTop: 16, justifyContent: "flex-end" }}>
          <button className="ghost" onClick={onClose}>Cancel</button>
          <button
            className="danger"
            autoFocus
            onClick={() => { onConfirm(); onClose(); }}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
