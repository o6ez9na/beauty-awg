"use client";

import { useState } from "react";
import { bandFor, hslToHex, hueFromHex, hueOf, readableTextColor, PICKER_SAT, PICKER_LIGHT } from "../lib/color";
import { useTheme } from "../lib/theme";
import Modal, { ModalFooter } from "./Modal";

// Full-hue gradient in the band the current theme actually renders cards in, so
// the slider previews what you will see rather than the stored value.
const track = (sat: number, light: number) =>
  `linear-gradient(to right, ${Array.from({ length: 13 }, (_, i) =>
    hslToHex(i * 30, sat, light)
  ).join(", ")})`;

export default function ColorPickerModal({
  title,
  current,
  seed,
  onSave,
  onClose,
}: {
  title: string;
  current: string; // stored override ("#rrggbb"), "" if unset
  seed: string; // address/id, for the auto-generated fallback hue
  onSave: (color: string) => void; // "" resets to the auto-generated color
  onClose: () => void;
}) {
  const [auto, setAuto] = useState(current === "");
  const [hue, setHue] = useState(current ? hueFromHex(current) : hueOf(seed));
  const { resolved } = useTheme();
  const band = bandFor(resolved);

  // Only the hue is identity. It is stored in one canonical band so the value in
  // the database doesn't depend on which theme happened to be open when it was
  // picked; every screen re-derives its own shade from that hue.
  const stored = hslToHex(hue, PICKER_SAT, PICKER_LIGHT);
  const swatchColor = hslToHex(auto ? hueOf(seed) : hue, band.sat, band.light);

  function save() {
    onSave(auto ? "" : stored);
    onClose();
  }

  return (
    <Modal
      title={title}
      subtitle="Only affects how it looks in this panel — nothing about the connection."
      onClose={onClose}
    >
      <div
        className="swatch"
        style={{ background: swatchColor, color: readableTextColor(swatchColor) }}
      >
        <span className="swatch-name">Preview</span>
        <span className="swatch-hex">{swatchColor}</span>
      </div>

      <div className="field">
        <label htmlFor="hue-slider">Color</label>
        <input
          id="hue-slider"
          type="range"
          min={0}
          max={359}
          value={hue}
          onChange={(e) => { setHue(Number(e.target.value)); setAuto(false); }}
          style={{ background: track(band.sat, band.light), opacity: auto ? 0.6 : 1 }}
          className="hue-slider"
        />
      </div>

      <div className="stack">
        <button className="ghost" onClick={() => { setAuto(true); setHue(hueOf(seed)); }} disabled={auto}>
          Back to the automatic color
        </button>
      </div>

      <ModalFooter>
        <button className="ghost" onClick={onClose}>Cancel</button>
        <button className="btn" onClick={save}>Save</button>
      </ModalFooter>
    </Modal>
  );
}
