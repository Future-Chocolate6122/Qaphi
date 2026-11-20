-- Esquema inicial: ajustes clave/valor.
-- Las marcas de tiempo se guardan como segundos Unix (INTEGER de 64 bits).

CREATE TABLE settings (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
