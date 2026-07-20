"use client";

import Modal, { ModalFooter } from "./Modal";

// Small yes/no gate for destructive actions (removing a device or location).
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
    <Modal title={title} onClose={onClose}>
      {body && <p className="prose">{body}</p>}
      <ModalFooter>
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="danger" onClick={() => { onConfirm(); onClose(); }}>
          {confirmLabel}
        </button>
      </ModalFooter>
    </Modal>
  );
}
