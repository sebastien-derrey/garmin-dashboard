package storage

import (
	"database/sql"
	"fmt"
	"time"

	"garmin_dashboard/garmin"
	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database
type DB struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database and sets up the schema
func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migration: %w", err)
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS hrv_data (
			date       TEXT PRIMARY KEY,
			last_night INTEGER,
			weekly_avg INTEGER,
			status     TEXT,
			synced_at  TEXT
		);

		CREATE TABLE IF NOT EXISTS training_status (
			date          TEXT PRIMARY KEY,
			atl           REAL,
			ctl           REAL,
			tsb           REAL,
			status_phrase TEXT,
			synced_at     TEXT
		);

		CREATE TABLE IF NOT EXISTS vo2max_data (
			date      TEXT PRIMARY KEY,
			vo2max    REAL,
			synced_at TEXT
		);
	`)
	return err
}

// ── HRV ───────────────────────────────────────────────────────────────────

func (d *DB) HasHRV(date string) bool {
	var n int
	d.db.QueryRow("SELECT COUNT(*) FROM hrv_data WHERE date = ?", date).Scan(&n)
	return n > 0
}

func (d *DB) SaveHRV(h *garmin.HRVSummary) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO hrv_data (date, last_night, weekly_avg, status, synced_at)
		 VALUES (?, ?, ?, ?, ?)`,
		h.CalendarDate, h.LastNight, h.WeeklyAvg, h.Status,
		time.Now().Format(time.RFC3339),
	)
	return err
}

// ── Training status ───────────────────────────────────────────────────────

func (d *DB) HasTrainingStatus(date string) bool {
	var n int
	d.db.QueryRow("SELECT COUNT(*) FROM training_status WHERE date = ?", date).Scan(&n)
	return n > 0
}

func (d *DB) SaveTrainingStatus(t *garmin.TrainingStatusEntry) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO training_status (date, atl, ctl, tsb, status_phrase, synced_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.CalendarDate,
		nullFloat(t.AcuteTrainingLoad),
		nullFloat(t.ChronicTrainingLoad),
		nullFloat(t.TrainingStressBalance),
		t.TrainingStatusPhrase,
		time.Now().Format(time.RFC3339),
	)
	return err
}

// ── VO2Max ────────────────────────────────────────────────────────────────

func (d *DB) SaveVO2MaxMap(m map[string]float64) error {
	for date, v := range m {
		if _, err := d.db.Exec(
			`INSERT OR REPLACE INTO vo2max_data (date, vo2max, synced_at) VALUES (?, ?, ?)`,
			date, v, time.Now().Format(time.RFC3339),
		); err != nil {
			return err
		}
	}
	return nil
}

// ── Query ─────────────────────────────────────────────────────────────────

// GetMetrics returns all cached metrics merged across the three tables
func (d *DB) GetMetrics(startDate, endDate string) ([]garmin.DailyMetrics, error) {
	rows, err := d.db.Query(`
		WITH dates AS (
			SELECT date FROM hrv_data        WHERE date >= ? AND date <= ?
			UNION
			SELECT date FROM training_status WHERE date >= ? AND date <= ?
			UNION
			SELECT date FROM vo2max_data     WHERE date >= ? AND date <= ?
		)
		SELECT
			d.date,
			h.last_night,
			h.status,
			t.atl,
			t.ctl,
			t.tsb,
			v.vo2max,
			t.status_phrase
		FROM dates d
		LEFT JOIN hrv_data        h ON h.date = d.date
		LEFT JOIN training_status t ON t.date = d.date
		LEFT JOIN vo2max_data     v ON v.date = d.date
		ORDER BY d.date ASC
	`, startDate, endDate, startDate, endDate, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

func scanMetrics(rows *sql.Rows) ([]garmin.DailyMetrics, error) {
	var out []garmin.DailyMetrics
	for rows.Next() {
		var m garmin.DailyMetrics
		var lastNight sql.NullInt64
		var hrvStatus, statusPhrase sql.NullString
		var atl, ctl, tsb, vo2max sql.NullFloat64

		if err := rows.Scan(&m.Date, &lastNight, &hrvStatus, &atl, &ctl, &tsb, &vo2max, &statusPhrase); err != nil {
			return nil, err
		}
		if lastNight.Valid {
			v := float64(lastNight.Int64)
			m.HRV = &v
		}
		if hrvStatus.Valid {
			m.HRVStatus = hrvStatus.String
		}
		if atl.Valid {
			m.ATL = &atl.Float64
		}
		if ctl.Valid {
			m.CTL = &ctl.Float64
		}
		if tsb.Valid {
			m.TSB = &tsb.Float64
		}
		if vo2max.Valid && vo2max.Float64 > 0 {
			m.VO2Max = &vo2max.Float64
		}
		if statusPhrase.Valid {
			m.Status = statusPhrase.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestSyncedDate returns the most recent date present in hrv_data or training_status
func (d *DB) LatestSyncedDate() string {
	var t sql.NullString
	d.db.QueryRow(`
		SELECT MAX(d) FROM (
			SELECT MAX(date) d FROM hrv_data
			UNION ALL
			SELECT MAX(date) d FROM training_status
		)
	`).Scan(&t)
	if t.Valid {
		return t.String
	}
	return ""
}

// EarliestSyncedDate returns the oldest date present across all tables
func (d *DB) EarliestSyncedDate() string {
	var t sql.NullString
	d.db.QueryRow(`
		SELECT MIN(d) FROM (
			SELECT MIN(date) d FROM hrv_data
			UNION ALL
			SELECT MIN(date) d FROM training_status
		)
	`).Scan(&t)
	if t.Valid {
		return t.String
	}
	return ""
}

// ClearAll truncates all data tables so the next sync fetches everything fresh
func (d *DB) ClearAll() error {
	_, err := d.db.Exec(`
		DELETE FROM hrv_data;
		DELETE FROM training_status;
		DELETE FROM vo2max_data;
	`)
	return err
}

// Stats returns diagnostic row counts and date ranges for each table
func (d *DB) Stats() map[string]interface{} {
	stats := map[string]interface{}{}
	for _, tbl := range []string{"hrv_data", "training_status", "vo2max_data"} {
		var count int
		var minDate, maxDate sql.NullString
		d.db.QueryRow(`SELECT COUNT(*), MIN(date), MAX(date) FROM `+tbl).Scan(&count, &minDate, &maxDate)
		stats[tbl] = map[string]interface{}{
			"rows":    count,
			"minDate": minDate.String,
			"maxDate": maxDate.String,
		}
	}
	return stats
}

// LastSync returns the most recent sync time across all tables
func (d *DB) LastSync() string {
	var t sql.NullString
	d.db.QueryRow(`
		SELECT MAX(s) FROM (
			SELECT MAX(synced_at) s FROM hrv_data
			UNION ALL
			SELECT MAX(synced_at)   FROM training_status
			UNION ALL
			SELECT MAX(synced_at)   FROM vo2max_data
		)
	`).Scan(&t)
	if t.Valid {
		return t.String
	}
	return ""
}

func nullFloat(v float64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
