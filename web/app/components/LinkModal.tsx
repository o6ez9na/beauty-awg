"use client";

import Modal, { ModalFooter } from "./Modal";

// Editor for a directed link between two locations. Site-to-site is genuinely
// an advanced feature, so the copy explains what the direction means in terms
// of "the devices at each place" rather than subnets and initiators.
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
    <Modal
      title="Link between two locations"
      subtitle={`${srcName} → ${dstName}`}
      onClose={onClose}
    >
      <p className="prose">
        Everything on <b>{srcName}</b>&rsquo;s network can open connections to everything on{" "}
        <b>{dstName}</b>&rsquo;s network — no app needed on the individual devices.
      </p>
      <p className="prose">
        {hasReverse
          ? `${dstName} can start connections back the other way too.`
          : `${dstName} can reply, but can't start a connection of its own.`}
      </p>

      <div className="stack">
        {hasReverse ? (
          <button className="ghost" onClick={() => { onRemoveReverse(); onClose(); }}>
            Stop {dstName} starting connections to {srcName}
          </button>
        ) : (
          <button className="ghost" onClick={() => { onAddReverse(); onClose(); }}>
            Also let {dstName} start connections to {srcName}
          </button>
        )}
      </div>

      <ModalFooter>
        <button className="ghost" onClick={onClose}>Close</button>
        <button className="danger" onClick={() => { onRemove(); onClose(); }}>Remove link</button>
      </ModalFooter>
    </Modal>
  );
}
