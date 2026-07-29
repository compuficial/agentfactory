package core

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rawDB creates <dir>/agentfactory.db with an arbitrary schema, the way
// a different af implementation sharing the data dir would.
func rawDB(t *testing.T, dir, ddl string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "agentfactory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = " + itoa(version)); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

const grokV2Schema = `
CREATE TABLE definitions (
  name TEXT PRIMARY KEY, harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
  work_dir TEXT NOT NULL DEFAULT '', env TEXT NOT NULL DEFAULT '{}',
  config TEXT NOT NULL DEFAULT '{}', service INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, definition TEXT NOT NULL DEFAULT '',
  harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', command TEXT NOT NULL DEFAULT '',
  work_dir TEXT NOT NULL, status TEXT NOT NULL, pid INTEGER NOT NULL DEFAULT 0,
  pgid INTEGER NOT NULL DEFAULT 0, exit_code INTEGER, log_path TEXT NOT NULL DEFAULT '',
  service INTEGER NOT NULL DEFAULT 0, started_at TEXT NOT NULL, last_active TEXT NOT NULL,
  ended_at TEXT, metadata TEXT NOT NULL DEFAULT '{}', env TEXT NOT NULL DEFAULT '{}',
  log_offset INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_sessions_name ON sessions(name);
`

func TestOpenStoreAcceptsForeignCompatibleSchema(t *testing.T) {
	// agentfactory-grok v2: different user_version, same column set.
	dir := t.TempDir()
	rawDB(t, dir, grokV2Schema, 2)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("compatible foreign schema must open: %v", err)
	}
	defer store.Close()
	sess := &AgentSession{
		ID: "abc123", Name: "x", Harness: "custom", Command: "true",
		WorkDir: "/", Status: StatusExited, LogPath: "/dev/null",
		StartedAt: time.Now(), LastActive: time.Now(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListSessions(true); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStoreRepairsMissingEnvColumn(t *testing.T) {
	// agentfactory-grok v1 lacked sessions.env.
	dir := t.TempDir()
	rawDB(t, dir, strings.Replace(grokV2Schema, " env TEXT NOT NULL DEFAULT '{}',\n  log_offset", " log_offset", 1), 1)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("env column should be added, got: %v", err)
	}
	defer store.Close()
	if _, err := store.ListSessions(true); err != nil {
		t.Fatalf("list after repair: %v", err)
	}
}

func TestOpenStoreRejectsIncompatibleSchema(t *testing.T) {
	dir := t.TempDir()
	rawDB(t, dir, strings.Replace(grokV2Schema, "log_offset INTEGER NOT NULL DEFAULT 0", "other INTEGER", 1), 3)
	_, err := OpenStore(dir)
	if ExitCode(err) != ExitEnv {
		t.Fatalf("incompatible schema must be exit 4, got: %v", err)
	}
	if !strings.Contains(err.Error(), "log_offset") || !strings.Contains(err.Error(), "AF_DATA_DIR") {
		t.Fatalf("error should name the missing column and suggest AF_DATA_DIR: %v", err)
	}
}
