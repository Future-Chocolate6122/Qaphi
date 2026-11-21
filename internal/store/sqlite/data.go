package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// ---------------------------------------------------------------- checks

const checkColumns = `id, name, type, group_name, interval_s, paused, params, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanCheck(sc scanner) (model.Check, error) {
	var (
		c                    model.Check
		intervalS            int64
		paused               int64
		params               string
		createdAt, updatedAt int64
	)
	if err := sc.Scan(&c.ID, &c.Name, &c.Type, &c.Group, &intervalS, &paused, &params, &createdAt, &updatedAt); err != nil {
		return model.Check{}, err
	}
	c.Interval = time.Duration(intervalS) * time.Second
	c.Paused = paused != 0
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	c.Params = module.Values{}
	if err := json.Unmarshal([]byte(params), &c.Params); err != nil {
		return model.Check{}, fmt.Errorf("parámetros del check %d ilegibles: %w", c.ID, err)
	}
	return c, nil
}

// ListChecks implementa store.Store.
func (s *Store) ListChecks(ctx context.Context) ([]model.Check, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkColumns+` FROM checks ORDER BY group_name, name, id`)
	if err != nil {
		return nil, fmt.Errorf("listar checks: %w", err)
	}
	defer rows.Close()
	var out []model.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheck implementa store.Store.
func (s *Store) GetCheck(ctx context.Context, id int64) (model.Check, error) {
	c, err := scanCheck(s.db.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM checks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Check{}, store.ErrNotFound
	}
	return c, err
}

func encodeParams(v module.Values) (string, error) {
	if v == nil {
		v = module.Values{}
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func intervalSeconds(d time.Duration) int64 {
	n := int64(d / time.Second)
	if n < 1 {
		n = 1
	}
	return n
}

// CreateCheck implementa store.Store.
func (s *Store) CreateCheck(ctx context.Context, c *model.Check) error {
	params, err := encodeParams(c.Params)
	if err != nil {
		return err
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO checks (name, type, group_name, interval_s, paused, params, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Type, c.Group, intervalSeconds(c.Interval), boolInt(c.Paused), params, now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("crear check: %w", err)
	}
	if c.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = time.Unix(now.Unix(), 0), time.Unix(now.Unix(), 0)
	return nil
}

// UpdateCheck implementa store.Store.
func (s *Store) UpdateCheck(ctx context.Context, c *model.Check) error {
	params, err := encodeParams(c.Params)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE checks SET name = ?, type = ?, group_name = ?, interval_s = ?, paused = ?, params = ?, updated_at = ?
		WHERE id = ?`,
		c.Name, c.Type, c.Group, intervalSeconds(c.Interval), boolInt(c.Paused), params, now, c.ID)
	if err != nil {
		return fmt.Errorf("actualizar check %d: %w", c.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	c.UpdatedAt = time.Unix(now, 0)
	return nil
}

// DeleteCheck implementa store.Store. Los resultados se borran en cascada.
func (s *Store) DeleteCheck(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar check %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- resultados

// InsertResults implementa store.Store. Todo el lote va en una transacción
// (una sola escritura secuencial en el WAL). Los resultados de checks que ya
// no existen se descartan sin error: pueden llegar del buffer tras un borrado.
func (s *Store) InsertResults(ctx context.Context, rs []model.Result) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("volcar resultados: %w", err)
	}
	defer tx.Rollback() // sin efecto si ya se confirmó
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO results (check_id, ts_ms, ok, latency_us, message)
		SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM checks WHERE id = ?)`)
	if err != nil {
		return fmt.Errorf("volcar resultados: %w", err)
	}
	defer stmt.Close()
	for _, r := range rs {
		if _, err := stmt.ExecContext(ctx, r.CheckID, r.At.UnixMilli(), boolInt(r.OK),
			r.Latency.Microseconds(), r.Message, r.CheckID); err != nil {
			return fmt.Errorf("volcar resultados: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("volcar resultados: %w", err)
	}
	return nil
}

// LastResults implementa store.Store: el último resultado guardado de cada
// check, para restaurar el estado al arrancar.
func (s *Store) LastResults(ctx context.Context) (map[int64]model.Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.check_id, r.ts_ms, r.ok, r.latency_us, r.message
		FROM results r
		JOIN (SELECT check_id, MAX(ts_ms) AS ts_ms FROM results GROUP BY check_id) m
		  ON m.check_id = r.check_id AND m.ts_ms = r.ts_ms`)
	if err != nil {
		return nil, fmt.Errorf("leer últimos resultados: %w", err)
	}
	defer rows.Close()
	out := map[int64]model.Result{}
	for rows.Next() {
		var (
			r               model.Result
			tsMs, latencyUs int64
			ok              int64
		)
		if err := rows.Scan(&r.CheckID, &tsMs, &ok, &latencyUs, &r.Message); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(tsMs)
		r.OK = ok != 0
		r.Latency = time.Duration(latencyUs) * time.Microsecond
		out[r.CheckID] = r
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- sesiones

// CreateSession implementa store.Store.
func (s *Store) CreateSession(ctx context.Context, sess model.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id_hash, csrf, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.IDHash, sess.CSRF, sess.CreatedAt.Unix(), sess.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("crear sesión: %w", err)
	}
	return nil
}

// GetSession implementa store.Store.
func (s *Store) GetSession(ctx context.Context, idHash []byte) (model.Session, error) {
	var (
		sess             model.Session
		created, expires int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT id_hash, csrf, created_at, expires_at FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&sess.IDHash, &sess.CSRF, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Session{}, store.ErrNotFound
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("leer sesión: %w", err)
	}
	sess.CreatedAt, sess.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	return sess, nil
}

// DeleteSession implementa store.Store.
func (s *Store) DeleteSession(ctx context.Context, idHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteAllSessions implementa store.Store.
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// DeleteExpiredSessions implementa store.Store.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
