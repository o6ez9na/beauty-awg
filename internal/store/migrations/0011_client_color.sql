-- Optional per-client display color (hex "#rrggbb") for the panel UI, same as
-- nodes.color: empty = unset, UI derives a color from the client's address.
ALTER TABLE clients ADD COLUMN color text NOT NULL DEFAULT '';
