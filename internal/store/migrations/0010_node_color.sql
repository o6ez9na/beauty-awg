-- Optional per-node display color (hex "#rrggbb") for the panel UI. Empty
-- string = unset, meaning the UI falls back to a color it derives from the
-- node's address, so this column only holds explicit overrides.
ALTER TABLE nodes ADD COLUMN color text NOT NULL DEFAULT '';
