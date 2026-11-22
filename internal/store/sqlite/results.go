package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// WriteBatch implementa store.Store. Todo el lote va en una transacción (una
// sola escritura secuencial en el WAL). Los datos de checks que ya no existen
// se descartan sin error: pueden llegar del buffer tras un borrado.
func (s *Store) WriteBatch(ctx context.Context, b store.Batch) error {
	if len(b.Results) == 0 && len(b.Hourly) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write batch: %w", err)
	}
	defer tx.Rollback() // sin efecto si ya se confirmó

	if len(b.Results) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT OR REPLACE INTO results (check_id, ts_ms, ok, state, latency_us, message)
			SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM checks WHERE id = ?)`)
		if err != nil {
			return fmt.Errorf("write batch: %w", err)
		}
		defer stmt.Close()
		for _, r := range b.Results {
			if _, err := stmt.ExecContext(ctx, r.CheckID, r.At.UnixMilli(), boolInt(r.OK), r.State.Code(),
				r.Latency.Microseconds(), r.Message, r.CheckID); err != nil {
				return fmt.Errorf("write batch: %w", err)
			}
		}
	}
	if len(b.Hourly) > 0 {
		// Los agregados se suman a los ya guardados para esa hora.
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO hourly (check_id, hour, total, down, degraded, lat_count, lat_sum_us, lat_min_us, lat_max_us)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM checks WHERE id = ?)
			ON CONFLICT (check_id, hour) DO UPDATE SET
				total = total + excluded.total,
				down = down + excluded.down,
				degraded = degraded + excluded.degraded,
				lat_sum_us = lat_sum_us + excluded.lat_sum_us,
				lat_min_us = CASE
					WHEN excluded.lat_count = 0 THEN lat_min_us
					WHEN lat_count = 0 THEN excluded.lat_min_us
					ELSE MIN(lat_min_us, excluded.lat_min_us) END,
				lat_max_us = MAX(lat_max_us, excluded.lat_max_us),
				lat_count = lat_count + excluded.lat_count`)
		if err != nil {
			return fmt.Errorf("write batch: %w", err)
		}
		defer stmt.Close()
		for _, h := range b.Hourly {
			if _, err := stmt.ExecContext(ctx, h.CheckID, h.Hour.Unix(), h.Total, h.Down, h.Degraded, h.LatCount,
				h.LatSum.Microseconds(), h.LatMin.Microseconds(), h.LatMax.Microseconds(), h.CheckID); err != nil {
				return fmt.Errorf("write batch: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("write batch: %w", err)
	}
	return nil
}

func scanResult(sc scanner) (model.Result, error) {
	var (
		r                       model.Result
		tsMs, latencyUs, ok, st int64
	)
	if err := sc.Scan(&r.CheckID, &tsMs, &ok, &st, &latencyUs, &r.Message); err != nil {
		return model.Result{}, err
	}
	r.At = time.UnixMilli(tsMs)
	r.OK = ok != 0
	r.State = model.StatusFromCode(st)
	r.Latency = time.Duration(latencyUs) * time.Microsecond
	return r, nil
}

// RecentResults implementa store.Store.
func (s *Store) RecentResults(ctx context.Context, n int) (map[int64][]model.Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT check_id, ts_ms, ok, state, latency_us, message FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY check_id ORDER BY ts_ms DESC) AS rn FROM results
		) WHERE rn <= ? ORDER BY check_id, ts_ms`, n)
	if err != nil {
		return nil, fmt.Errorf("read recent results: %w", err)
	}
	defer rows.Close()
	out := map[int64][]model.Result{}
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out[r.CheckID] = append(out[r.CheckID], r)
	}
	return out, rows.Err()
}

// ResultsRange implementa store.Store.
func (s *Store) ResultsRange(ctx context.Context, checkID int64, from, to time.Time) ([]model.Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT check_id, ts_ms, ok, state, latency_us, message FROM results
		WHERE check_id = ? AND ts_ms >= ? AND ts_ms < ? ORDER BY ts_ms`,
		checkID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read results: %w", err)
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResultBuckets implementa store.Store. La agregación se hace en SQLite para
// no traer miles de filas a memoria.
func (s *Store) ResultBuckets(ctx context.Context, checkID int64, from, to time.Time, step time.Duration) ([]model.Bucket, error) {
	stepMs := step.Milliseconds()
	if stepMs <= 0 {
		return nil, errors.New("invalid bucket step")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT (ts_ms - ?) / ? AS b, COUNT(*),
			SUM(CASE WHEN state = 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN state = 3 THEN 1 ELSE 0 END),
			SUM(ok),
			COALESCE(AVG(CASE WHEN ok = 1 THEN latency_us END), 0),
			COALESCE(MAX(CASE WHEN ok = 1 THEN latency_us END), 0)
		FROM results
		WHERE check_id = ? AND ts_ms >= ? AND ts_ms < ? AND state != 4
		GROUP BY b ORDER BY b`,
		from.UnixMilli(), stepMs, checkID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read result buckets: %w", err)
	}
	defer rows.Close()
	var out []model.Bucket
	for rows.Next() {
		var (
			b     model.Bucket
			idx   int64
			avgUs float64
			maxUs int64
		)
		if err := rows.Scan(&idx, &b.Total, &b.Down, &b.Degraded, &b.OK, &avgUs, &maxUs); err != nil {
			return nil, err
		}
		b.Start = from.Add(time.Duration(idx) * step)
		b.AvgLat = time.Duration(avgUs) * time.Microsecond
		b.MaxLat = time.Duration(maxUs) * time.Microsecond
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanHourly(sc scanner) (model.Hourly, error) {
	var (
		h                       model.Hourly
		hour, sum, minUs, maxUs int64
	)
	if err := sc.Scan(&h.CheckID, &hour, &h.Total, &h.Down, &h.Degraded, &h.LatCount, &sum, &minUs, &maxUs); err != nil {
		return model.Hourly{}, err
	}
	h.Hour = time.Unix(hour, 0).UTC()
	h.LatSum = time.Duration(sum) * time.Microsecond
	h.LatMin = time.Duration(minUs) * time.Microsecond
	h.LatMax = time.Duration(maxUs) * time.Microsecond
	return h, nil
}

// HourlyRange implementa store.Store.
func (s *Store) HourlyRange(ctx context.Context, checkID int64, from, to time.Time) ([]model.Hourly, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT check_id, hour, total, down, degraded, lat_count, lat_sum_us, lat_min_us, lat_max_us
		FROM hourly WHERE check_id = ? AND hour >= ? AND hour < ? ORDER BY hour`,
		checkID, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("read hourly data: %w", err)
	}
	defer rows.Close()
	var out []model.Hourly
	for rows.Next() {
		h, err := scanHourly(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HourlyTotals implementa store.Store.
func (s *Store) HourlyTotals(ctx context.Context, since time.Time) (map[int64]model.Hourly, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT check_id, 0, SUM(total), SUM(down), SUM(degraded), SUM(lat_count), SUM(lat_sum_us),
			COALESCE(MIN(CASE WHEN lat_count > 0 THEN lat_min_us END), 0), MAX(lat_max_us)
		FROM hourly WHERE hour >= ? GROUP BY check_id`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("read hourly totals: %w", err)
	}
	defer rows.Close()
	out := map[int64]model.Hourly{}
	for rows.Next() {
		h, err := scanHourly(rows)
		if err != nil {
			return nil, err
		}
		h.Hour = since
		out[h.CheckID] = h
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- incidencias

// OpenIncident implementa store.Store.
func (s *Store) OpenIncident(ctx context.Context, i *model.Incident) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO incidents (check_id, started_at, last_error) VALUES (?, ?, ?)`,
		i.CheckID, i.Start.UnixMilli(), i.LastError)
	if err != nil {
		return fmt.Errorf("open incident: %w", err)
	}
	i.ID, err = res.LastInsertId()
	return err
}

// CloseIncident implementa store.Store.
func (s *Store) CloseIncident(ctx context.Context, id int64, end time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE incidents SET ended_at = ?, last_error = ? WHERE id = ?`,
		end.UnixMilli(), lastError, id)
	if err != nil {
		return fmt.Errorf("close incident %d: %w", id, err)
	}
	return nil
}

func scanIncident(sc scanner) (model.Incident, error) {
	var (
		i     model.Incident
		start int64
		end   sql.NullInt64
	)
	if err := sc.Scan(&i.ID, &i.CheckID, &start, &end, &i.LastError); err != nil {
		return model.Incident{}, err
	}
	i.Start = time.UnixMilli(start)
	if end.Valid {
		i.End = time.UnixMilli(end.Int64)
	}
	return i, nil
}

// OpenIncidents implementa store.Store.
func (s *Store) OpenIncidents(ctx context.Context) (map[int64]model.Incident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, check_id, started_at, ended_at, last_error FROM incidents WHERE ended_at IS NULL ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("read open incidents: %w", err)
	}
	defer rows.Close()
	out := map[int64]model.Incident{}
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out[i.CheckID] = i
	}
	return out, rows.Err()
}

// ListIncidents implementa store.Store: las más recientes primero.
func (s *Store) ListIncidents(ctx context.Context, checkID int64, limit int) ([]model.Incident, error) {
	q := `SELECT id, check_id, started_at, ended_at, last_error FROM incidents`
	args := []any{}
	if checkID != 0 {
		q += ` WHERE check_id = ?`
		args = append(args, checkID)
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	var out []model.Incident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- retención

func (s *Store) deleteBefore(ctx context.Context, query string, arg int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, query, arg)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteResultsBefore implementa store.Store.
func (s *Store) DeleteResultsBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteBefore(ctx, `DELETE FROM results WHERE ts_ms < ?`, before.UnixMilli())
}

// DeleteHourlyBefore implementa store.Store.
func (s *Store) DeleteHourlyBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteBefore(ctx, `DELETE FROM hourly WHERE hour < ?`, before.Unix())
}

// DeleteIncidentsBefore implementa store.Store: solo las ya cerradas.
func (s *Store) DeleteIncidentsBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteBefore(ctx, `DELETE FROM incidents WHERE ended_at IS NOT NULL AND ended_at < ?`, before.UnixMilli())
}
