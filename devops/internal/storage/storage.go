// Package storage persists activity history, favorites and the command search
// index in a local SQLite database. Secrets are redacted before they get here,
// and command output is only stored when history.store_command_output is on.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	fts bool
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	session     TEXT NOT NULL,
	job_id      INTEGER NOT NULL,
	name        TEXT NOT NULL,
	command_id  TEXT,
	args        TEXT,
	profile     TEXT,
	account     TEXT,
	region      TEXT,
	kind        TEXT,
	risk        TEXT,
	status      TEXT,
	started_at  INTEGER,
	ended_at    INTEGER,
	exit_code   INTEGER,
	error       TEXT,
	resources   TEXT,
	output      TEXT
);
CREATE INDEX IF NOT EXISTS jobs_started ON jobs(started_at);
CREATE TABLE IF NOT EXISTS favorites (
	command_id TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL
);`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	s := &Store{db: db}
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS command_index USING fts5(
		id UNINDEXED, name, category, description, tags, tokenize='porter unicode61')`)
	s.fts = err == nil
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) FTSEnabled() bool { return s.fts }

type JobRecord struct {
	Session   string
	JobID     int64
	Name      string
	CommandID string
	Args      map[string]string
	Profile   string
	Account   string
	Region    string
	Kind      string
	Risk      string
	Status    string
	Started   time.Time
	Ended     time.Time
	ExitCode  *int
	Error     string
	Resources []string
	Output    string
}

func (s *Store) RecordJob(r JobRecord) error {
	args, _ := json.Marshal(r.Args)
	res, _ := json.Marshal(r.Resources)
	var exit any
	if r.ExitCode != nil {
		exit = *r.ExitCode
	}
	_, err := s.db.Exec(`INSERT INTO jobs (session, job_id, name, command_id, args, profile, account, region, kind, risk,
		status, started_at, ended_at, exit_code, error, resources, output) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Session, r.JobID, r.Name, r.CommandID, string(args), r.Profile, r.Account, r.Region, r.Kind, r.Risk,
		r.Status, r.Started.UnixMilli(), r.Ended.UnixMilli(), exit, r.Error, string(res), r.Output)
	return err
}

// RecentJobs returns persisted jobs from earlier sessions, newest first.
func (s *Store) RecentJobs(excludeSession string, limit int) ([]JobRecord, error) {
	rows, err := s.db.Query(`SELECT session, job_id, name, command_id, args, profile, account, region, kind, risk, status,
		started_at, ended_at, exit_code, error, resources FROM jobs WHERE session != ? ORDER BY started_at DESC LIMIT ?`,
		excludeSession, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRecord
	for rows.Next() {
		var (
			r               JobRecord
			args, res, errS sql.NullString
			cmdID           sql.NullString
			started, ended  int64
			exit            sql.NullInt64
		)
		if err := rows.Scan(&r.Session, &r.JobID, &r.Name, &cmdID, &args, &r.Profile, &r.Account, &r.Region, &r.Kind,
			&r.Risk, &r.Status, &started, &ended, &exit, &errS, &res); err != nil {
			return nil, err
		}
		r.CommandID, r.Error = cmdID.String, errS.String
		r.Started, r.Ended = time.UnixMilli(started), time.UnixMilli(ended)
		if exit.Valid {
			c := int(exit.Int64)
			r.ExitCode = &c
		}
		_ = json.Unmarshal([]byte(args.String), &r.Args)
		_ = json.Unmarshal([]byte(res.String), &r.Resources)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageCounts returns how often each command was run (for search ranking).
func (s *Store) UsageCounts() map[string]int {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT command_id, COUNT(*) FROM jobs WHERE command_id != '' GROUP BY command_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out
}

// Prune deletes history older than the retention window.
func (s *Store) Prune(days int) error {
	if days <= 0 {
		_, err := s.db.Exec(`DELETE FROM jobs`)
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	_, err := s.db.Exec(`DELETE FROM jobs WHERE started_at < ?`, cutoff)
	return err
}

func (s *Store) Favorites() map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT command_id FROM favorites`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// ToggleFavorite flips a favorite and returns the new state.
func (s *Store) ToggleFavorite(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM favorites WHERE command_id = ?`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = s.db.Exec(`INSERT INTO favorites (command_id, created_at) VALUES (?, ?)`, id, time.Now().UnixMilli())
	return err == nil, err
}

type IndexDoc struct {
	ID, Name, Category, Description, Tags string
}

// IndexCommands rebuilds the FTS5 command index.
func (s *Store) IndexCommands(docs []IndexDoc) error {
	if !s.fts {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM command_index`); err != nil {
		return err
	}
	for _, d := range docs {
		if _, err := tx.Exec(`INSERT INTO command_index (id, name, category, description, tags) VALUES (?,?,?,?,?)`,
			d.ID, d.Name, d.Category, d.Description, d.Tags); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchCommands runs a prefix full-text query and returns IDs by relevance.
func (s *Store) SearchCommands(q string, limit int) ([]string, error) {
	if !s.fts {
		return nil, nil
	}
	var terms []string
	for _, t := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(t) > 1 {
			terms = append(terms, `"`+t+`"*`)
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id FROM command_index WHERE command_index MATCH ?
		ORDER BY bm25(command_index, 0, 10.0, 3.0, 1.0, 5.0) LIMIT ?`, strings.Join(terms, " OR "), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}
