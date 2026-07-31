package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	// modernc.org/sqlite registers the pure-Go "sqlite" driver with
	// database/sql; imported for its registration side effect only.
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// colHarness is the harness column name, referenced by the schema-shape
// checks that guard against a data dir shared with another af build.
const colHarness = "harness"

// Permissions for everything af creates on disk. All of it is
// single-user (the data dir, session logs, harness wiring files), so
// owner-only files and directories are the tightest safe modes.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

const schema = `
CREATE TABLE IF NOT EXISTS definitions (
	name     TEXT PRIMARY KEY,
	harness  TEXT NOT NULL,
	model    TEXT NOT NULL DEFAULT '',
	work_dir TEXT NOT NULL DEFAULT '',
	env      TEXT NOT NULL DEFAULT '{}',
	config   TEXT NOT NULL DEFAULT '{}',
	service  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	definition  TEXT NOT NULL DEFAULT '',
	harness     TEXT NOT NULL,
	model       TEXT NOT NULL DEFAULT '',
	command     TEXT NOT NULL,
	work_dir    TEXT NOT NULL,
	status      TEXT NOT NULL,
	pid         INTEGER NOT NULL DEFAULT 0,
	pgid        INTEGER NOT NULL DEFAULT 0,
	exit_code   INTEGER,
	log_path    TEXT NOT NULL,
	service     INTEGER NOT NULL DEFAULT 0,
	started_at  TEXT NOT NULL,
	last_active TEXT NOT NULL,
	ended_at    TEXT,
	metadata    TEXT NOT NULL DEFAULT '{}',
	env         TEXT NOT NULL DEFAULT '{}',
	log_offset  INTEGER NOT NULL DEFAULT 0,
	status_origin TEXT NOT NULL DEFAULT ''
);
`

// Store is the SQLite-backed source of truth for identity, metadata,
// and history. One write connection, WAL, and a 5s busy timeout.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) <dataDir>/agentfactory.db and
// ensures the schema. Errors are exit-4 environment problems.
func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, dirPerm); err != nil {
		return nil, Errf(ExitEnv, "create data dir %s: %v", dataDir, err)
	}
	if err := os.Chmod(dataDir, dirPerm); err != nil {
		return nil, Errf(ExitEnv, "secure data dir %s: %v", dataDir, err)
	}
	dbPath := filepath.Join(dataDir, "agentfactory.db")
	dbURL := url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}
	dsn := dbURL.String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, Errf(ExitEnv, "open db: %v", err)
	}
	db.SetMaxOpenConns(1) // one write connection
	var version int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, Errf(ExitEnv, "read db version: %v", err)
	}
	if err := secureStoreFiles(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	var prepareErr error
	if version == 0 {
		prepareErr = initializeStore(db)
	} else {
		prepareErr = prepareExistingStore(db, dbPath, version)
	}
	if prepareErr != nil {
		_ = db.Close()
		return nil, prepareErr
	}
	if err := secureStoreFiles(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func initializeStore(db *sql.DB) error {
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		return Errf(ExitEnv, "create schema: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return Errf(ExitEnv, "set db version: %v", err)
	}
	return nil
}

func prepareExistingStore(db *sql.DB, dbPath string, version int) error {
	// Existing DB. The data dir may be shared with another af
	// implementation (agentfactory-grok) that versions the same file
	// differently, so judge compatibility by shape, not version number:
	// repair the one known gap (their v1 lacked sessions.env), then
	// require every column we read to exist.
	if err := ensureColumn(db, "sessions", "env", `TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return Errf(ExitEnv, "migrate sessions.env: %v", err)
	}
	// Pre-coordination DBs (and agentfactory-grok's) lack status_origin.
	if err := ensureColumn(db, "sessions", "status_origin", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return Errf(ExitEnv, "migrate sessions.status_origin: %v", err)
	}
	// v1.2.1 renamed the `active` status to `working`; normalize rows
	// written by older builds (or by another af build sharing this data
	// dir). Idempotent, and reconciliation would converge them anyway —
	// this just keeps status names consistent for --json readers.
	if _, err := db.ExecContext(context.Background(), `UPDATE sessions SET status='working' WHERE status='active'`); err != nil {
		return Errf(ExitEnv, "normalize legacy status rows: %v", err)
	}
	for table, cols := range map[string][]string{
		"sessions": {
			"id", "name", "definition", colHarness, "model", "command", "work_dir",
			"status", "pid", "pgid", "exit_code", "log_path", "service", "started_at",
			"last_active", "ended_at", "metadata", "env", "log_offset", "status_origin",
		},
		"definitions": {"name", colHarness, "model", "work_dir", "env", "config", "service"},
	} {
		have, err := tableColumns(db, table)
		if err != nil {
			return Errf(ExitEnv, "inspect table %s: %v", table, err)
		}
		for _, col := range cols {
			if !have[col] {
				return Errf(ExitEnv,
					"db %s has an incompatible schema (version %d, missing %s.%s) — "+
						"if this data dir is shared with another af build, point this one elsewhere via AF_DATA_DIR",
					dbPath, version, table, col)
			}
		}
	}
	return nil
}

func secureStoreFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Chmod(path, filePerm); err != nil && !os.IsNotExist(err) {
			return Errf(ExitEnv, "secure db file %s: %v", path, err)
		}
	}
	return nil
}

// SchemaVersion reports the db's PRAGMA user_version alongside the
// version this build writes. Doctor reports a mismatch; OpenStore
// deliberately judges compatibility by shape instead.
func (s *Store) SchemaVersion() (have, want int, err error) {
	err = s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&have)
	return have, schemaVersion, err
}

// tableColumns returns the set of column names of table (empty if the
// table does not exist).
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(context.Background(), fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// ensureColumn adds col to table if missing (no-op if the table itself
// is missing; the shape check reports that case).
func ensureColumn(db *sql.DB, table, col, decl string) error {
	have, err := tableColumns(db, table)
	if err != nil {
		return err
	}
	if len(have) == 0 || have[col] {
		return nil
	}
	_, err = db.ExecContext(context.Background(), fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, col, decl))
	return err
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// --- helpers ---

func jsonMap(m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", Errf(ExitRuntime, "encode string map: %v", err)
	}
	return string(b), nil
}

func parseMap(s string) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// Times are stored RFC3339 with nanoseconds; sub-second precision
// matters for the T1 idle heuristic. time.Parse(RFC3339, ...) accepts
// the fractional part, and JSON output formats without it.
func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- definitions ---

// PutDefinition inserts or replaces a definition by name.
func (s *Store) PutDefinition(d *AgentDefinition) error {
	env, err := jsonMap(d.Env)
	if err != nil {
		return err
	}
	cfg, err := jsonMap(d.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(context.Background(), `INSERT INTO definitions (name, harness, model, work_dir, env, config, service)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET harness=excluded.harness, model=excluded.model,
			work_dir=excluded.work_dir, env=excluded.env, config=excluded.config, service=excluded.service`,
		d.Name, d.Harness, d.Model, d.WorkDir, env, cfg, boolInt(d.Service))
	if err != nil {
		return Errf(ExitRuntime, "save definition: %v", err)
	}
	return nil
}

// GetDefinition loads one definition; exit-3 error if absent.
func (s *Store) GetDefinition(name string) (*AgentDefinition, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT name, harness, model, work_dir, env, config, service
		FROM definitions WHERE name = ?`, name)
	var d AgentDefinition
	var env, cfg string
	var service int
	if err := row.Scan(&d.Name, &d.Harness, &d.Model, &d.WorkDir, &env, &cfg, &service); err != nil {
		if err == sql.ErrNoRows {
			return nil, Errf(ExitNotFound, "definition %q not found", name)
		}
		return nil, Errf(ExitRuntime, "load definition: %v", err)
	}
	d.Env, d.Config, d.Service = parseMap(env), parseMap(cfg), service != 0
	return &d, nil
}

// ListDefinitions returns all definitions ordered by name.
func (s *Store) ListDefinitions() ([]*AgentDefinition, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT name, harness, model, work_dir, env, config, service
		FROM definitions ORDER BY name`)
	if err != nil {
		return nil, Errf(ExitRuntime, "list definitions: %v", err)
	}
	defer rows.Close()
	var out []*AgentDefinition
	for rows.Next() {
		var d AgentDefinition
		var env, cfg string
		var service int
		if err := rows.Scan(&d.Name, &d.Harness, &d.Model, &d.WorkDir, &env, &cfg, &service); err != nil {
			return nil, Errf(ExitRuntime, "scan definition: %v", err)
		}
		d.Env, d.Config, d.Service = parseMap(env), parseMap(cfg), service != 0
		out = append(out, &d)
	}
	return out, rows.Err()
}

// DeleteDefinition removes a definition; exit-3 error if absent.
func (s *Store) DeleteDefinition(name string) error {
	res, err := s.db.ExecContext(context.Background(), `DELETE FROM definitions WHERE name = ?`, name)
	if err != nil {
		return Errf(ExitRuntime, "delete definition: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Errf(ExitNotFound, "definition %q not found", name)
	}
	return nil
}

// --- sessions ---

const sessionCols = `id, name, definition, harness, model, command, work_dir, status,
	pid, pgid, exit_code, log_path, service, started_at, last_active, ended_at,
	metadata, env, log_offset, status_origin`

// InsertSession stores a new session row.
func (s *Store) InsertSession(a *AgentSession) error {
	metadata, err := jsonMap(a.Metadata)
	if err != nil {
		return err
	}
	env, err := jsonMap(a.Env)
	if err != nil {
		return err
	}
	var ended any
	if a.EndedAt != nil {
		ended = fmtTime(*a.EndedAt)
	}
	_, err = s.db.ExecContext(context.Background(), `INSERT INTO sessions (`+sessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Definition, a.Harness, a.Model, a.Command, a.WorkDir, string(a.Status),
		a.PID, a.PGID, a.ExitCode, a.LogPath, boolInt(a.Service), fmtTime(a.StartedAt),
		fmtTime(a.LastActive), ended, metadata, env, a.LogOffset, a.StatusOrigin)
	if err != nil {
		return Errf(ExitRuntime, "insert session: %v", err)
	}
	return nil
}

// UpdateSession rewrites a session row by ID.
func (s *Store) UpdateSession(a *AgentSession) error {
	metadata, err := jsonMap(a.Metadata)
	if err != nil {
		return err
	}
	env, err := jsonMap(a.Env)
	if err != nil {
		return err
	}
	var ended any
	if a.EndedAt != nil {
		ended = fmtTime(*a.EndedAt)
	}
	result, err := s.db.ExecContext(context.Background(), `UPDATE sessions SET name=?, definition=?, harness=?, model=?, command=?,
		work_dir=?, status=?, pid=?, pgid=?, exit_code=?, log_path=?, service=?, started_at=?,
		last_active=?, ended_at=?, metadata=?, env=?, log_offset=?, status_origin=? WHERE id=?`,
		a.Name, a.Definition, a.Harness, a.Model, a.Command, a.WorkDir, string(a.Status),
		a.PID, a.PGID, a.ExitCode, a.LogPath, boolInt(a.Service), fmtTime(a.StartedAt),
		fmtTime(a.LastActive), ended, metadata, env, a.LogOffset, a.StatusOrigin, a.ID)
	if err != nil {
		return Errf(ExitRuntime, "update session: %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Errf(ExitRuntime, "count updated session rows: %v", err)
	}
	if rows == 0 {
		return Errf(ExitNotFound, "session %q not found", a.ID)
	}
	return nil
}

// UpdateSignal records an explicit harness signal without rewriting
// unrelated fields from a stale session snapshot. Terminal rows win races.
func (s *Store) UpdateSignal(id string, status Status, logOffset int64) (bool, error) {
	result, err := s.db.ExecContext(context.Background(), `UPDATE sessions
		SET status=?, status_origin=?, log_offset=max(log_offset, ?)
		WHERE id=? AND status NOT IN ('exited','failed')`,
		string(status), OriginSignal, logOffset, id)
	if err != nil {
		return false, Errf(ExitRuntime, "signal session: %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, Errf(ExitRuntime, "count signaled session rows: %v", err)
	}
	return rows > 0, nil
}

// UpdateReconciledSession persists a nonterminal reconciliation result only
// when the row still matches the snapshot that produced it.
func (s *Store) UpdateReconciledSession(a *AgentSession, observedStatus Status, observedOrigin string, observedOffset int64) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE sessions
		SET status=?, last_active=?, log_offset=?, status_origin=?
		WHERE id=? AND status=? AND status_origin=? AND log_offset=?`,
		string(a.Status), fmtTime(a.LastActive), a.LogOffset, a.StatusOrigin,
		a.ID, string(observedStatus), observedOrigin, observedOffset)
	if err != nil {
		return Errf(ExitRuntime, "reconcile session: %v", err)
	}
	return nil
}

func scanSession(scan func(...any) error) (*AgentSession, error) {
	var a AgentSession
	var status, started, lastActive, meta, env string
	var ended sql.NullString
	var exitCode sql.NullInt64
	var service int
	err := scan(&a.ID, &a.Name, &a.Definition, &a.Harness, &a.Model, &a.Command, &a.WorkDir,
		&status, &a.PID, &a.PGID, &exitCode, &a.LogPath, &service, &started, &lastActive,
		&ended, &meta, &env, &a.LogOffset, &a.StatusOrigin)
	if err != nil {
		return nil, err
	}
	a.Status = Status(status)
	a.Service = service != 0
	a.StartedAt = parseTime(started)
	a.LastActive = parseTime(lastActive)
	if ended.Valid {
		t := parseTime(ended.String)
		a.EndedAt = &t
	}
	if exitCode.Valid {
		c := int(exitCode.Int64)
		a.ExitCode = &c
	}
	a.Metadata = parseMap(meta)
	a.Env = parseMap(env)
	return &a, nil
}

// GetSession loads one session by exact ID; exit-3 error if absent.
func (s *Store) GetSession(id string) (*AgentSession, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id)
	a, err := scanSession(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Errf(ExitNotFound, "session %q not found", id)
	}
	if err != nil {
		return nil, Errf(ExitRuntime, "load session: %v", err)
	}
	return a, nil
}

// ListSessions returns sessions ordered by StartedAt; terminal ones
// only when all is true.
func (s *Store) ListSessions(all bool) ([]*AgentSession, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions`
	if !all {
		q += ` WHERE status NOT IN ('exited','failed')`
	}
	q += ` ORDER BY started_at, id`
	rows, err := s.db.QueryContext(context.Background(), q)
	if err != nil {
		return nil, Errf(ExitRuntime, "list sessions: %v", err)
	}
	defer rows.Close()
	var out []*AgentSession
	for rows.Next() {
		a, err := scanSession(rows.Scan)
		if err != nil {
			return nil, Errf(ExitRuntime, "scan session: %v", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteSession removes a session row; exit-3 error if absent.
func (s *Store) DeleteSession(id string) error {
	res, err := s.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return Errf(ExitRuntime, "delete session: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Errf(ExitNotFound, "session %q not found", id)
	}
	return nil
}

// FindSessions resolves an ID-or-name reference to matching sessions.
// Exact ID match wins; otherwise name matches (live first).
func (s *Store) FindSessions(ref string) ([]*AgentSession, error) {
	if a, err := s.GetSession(ref); err == nil {
		return []*AgentSession{a}, nil
	} else if ExitCode(err) != ExitNotFound {
		return nil, err
	}
	all, err := s.ListSessions(true)
	if err != nil {
		return nil, err
	}
	var live, dead []*AgentSession
	for _, a := range all {
		if a.Name != ref {
			continue
		}
		if a.Status.Terminal() {
			dead = append(dead, a)
		} else {
			live = append(live, a)
		}
	}
	if len(live) > 0 {
		return live, nil
	}
	return dead, nil
}
