-- Checks ocultos en la página pública (sin sesión).

ALTER TABLE checks ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;
