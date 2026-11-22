-- Confirmación de fallos, umbral de lentitud, estado de cada pulso,
-- incidencias y agregados por hora.

ALTER TABLE checks ADD COLUMN fail_threshold INTEGER NOT NULL DEFAULT 3;
ALTER TABLE checks ADD COLUMN degraded_ms INTEGER NOT NULL DEFAULT 0;

-- Estado del check tras cada pulso: 0 caído, 1 operativo, 2 pendiente,
-- 3 lento, 4 sin permisos.
ALTER TABLE results ADD COLUMN state INTEGER NOT NULL DEFAULT 1;
UPDATE results SET state = ok;

CREATE INDEX results_ts ON results (ts_ms);

-- Incidencias: started_at/ended_at en milisegundos; ended_at NULL = abierta.
CREATE TABLE incidents (
    id         INTEGER PRIMARY KEY,
    check_id   INTEGER NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,
    last_error TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX incidents_check ON incidents (check_id, started_at);
CREATE INDEX incidents_started ON incidents (started_at);

-- Agregados por hora (hour = inicio de la hora en segundos Unix, UTC).
-- Latencias en microsegundos, solo de los pulsos correctos.
CREATE TABLE hourly (
    check_id     INTEGER NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    hour         INTEGER NOT NULL,
    total        INTEGER NOT NULL,
    down         INTEGER NOT NULL,
    degraded     INTEGER NOT NULL,
    lat_count    INTEGER NOT NULL,
    lat_sum_us   INTEGER NOT NULL,
    lat_min_us   INTEGER NOT NULL,
    lat_max_us   INTEGER NOT NULL,
    PRIMARY KEY (check_id, hour)
) STRICT, WITHOUT ROWID;

CREATE INDEX hourly_hour ON hourly (hour);
