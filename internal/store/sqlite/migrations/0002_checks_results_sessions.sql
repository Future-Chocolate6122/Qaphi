-- Checks, resultados en bruto y sesiones.
-- Marcas de tiempo de checks y sesiones en segundos Unix; las de resultados
-- en milisegundos (ordenación fina). Latencias en microsegundos.

CREATE TABLE checks (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    type       TEXT    NOT NULL,
    group_name TEXT    NOT NULL DEFAULT '',
    interval_s INTEGER NOT NULL CHECK (interval_s > 0),
    paused     INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    params     TEXT    NOT NULL DEFAULT '{}', -- JSON de module.Values
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE results (
    check_id   INTEGER NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    ts_ms      INTEGER NOT NULL,
    ok         INTEGER NOT NULL CHECK (ok IN (0, 1)),
    latency_us INTEGER NOT NULL,
    message    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (check_id, ts_ms)
) STRICT, WITHOUT ROWID;

CREATE TABLE sessions (
    id_hash    BLOB    NOT NULL PRIMARY KEY,
    csrf       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX sessions_expires ON sessions (expires_at);
