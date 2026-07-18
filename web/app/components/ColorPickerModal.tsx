"use client";

import { useState } from "react";
import { hslToHex, hueFromHex, hueOf, readableTextColor, PICKER_SAT, PICKER_LIGHT } from "../lib/color";

// Full-hue gradient at the same saturation/lightness every node's color is
// drawn from, so whatever the slider lands on stays in the same readable band.
const TRACK = `linear-gradient(to right, ${Array.from({ length: 13 }, (_, i) =>
  hslToHex(i * 30, PICKER_SAT, PICKER_LIGHT)
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
  seed: string; // node address/id, for the auto-generated fallback hue
  onSave: (color: string) => void; // "" resets to the auto-generated color
  onClose: () => void;
}) {
  const [auto, setAuto] = useState(current === "");
  const [hue, setHue] = useState(current ? hueFromHex(current) : hueOf(seed));

  const preview = hslToHex(hue, PICKER_SAT, PICKER_LIGHT);
  const swatchColor = auto ? hslToHex(hueOf(seed), PICKER_SAT, PICKER_LIGHT) : preview;

  function save() {
    onSave(auto ? "" : preview);
    onClose();
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ textAlign: "left" }} onClick={(e) => e.stopPropagation()}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>

        <div
          className="gnode"
          style={{
            background: swatchColor,
            color: readableTextColor(swatchColor),
            borderColor: "transparent",
            marginBottom: 14,
            cursor: "default",
          }}
        >
          <div className="gnode-title">Aa — preview</div>
          <div className="gnode-sub" style={{ opacity: 0.85 }}>{swatchColor}</div>
        </div>

        <div className="field">
          <label>Hue</label>
          <input
            type="range"
            min={0}
            max={359}
            value={hue}
            onChange={(e) => { setHue(Number(e.target.value)); setAuto(false); }}
            style={{ width: "100%", background: TRACK, opacity: auto ? 0.6 : 1 }}
            className="hue-slider"
          />
        </div>

        <div className="row" style={{ marginTop: 4 }}>
          <button className="ghost" onClick={() => { setAuto(true); setHue(hueOf(seed)); }} disabled={auto}>
            Reset to auto-generated
          </button>
        </div>

        <div className="row" style={{ marginTop: 16, justifyContent: "flex-end" }}>
          <button className="ghost" onClick={onClose}>Cancel</button>
          <button className="btn" onClick={save}>Save</button>
        </div>
      </div>
    </div>
  );
}
