package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mockBackend implements SessionBackend for unit tests.
type mockBackend struct {
	alive    map[string]bool
	dead     map[string]int // id -> exit code (present = pane dead)
	killed   []string
	sent     []string
	captured string
}

func newMockBackend() *mockBackend {
	return &mockBackend{alive: map[string]bool{}, dead: map[string]int{}}
}

func (m *mockBackend) Create(sess *AgentSession) error {
	m.alive[sess.ID] = true
	sess.PID = 12345
	sess.PGID = 12345
	return nil
}
func (m *mockBackend) Attach(id string) error { return nil }
func (m *mockBackend) CapturePane(id string, lines int) (string, error) {
	return m.captured, nil
}

func (m *mockBackend) SendKeys(id, input string, enter bool) error {
	m.sent = append(m.sent, input)
	return nil
}
func (m *mockBackend) IsAlive(id string) (bool, error) { return m.alive[id], nil }
func (m *mockBackend) DeadStatus(id string) (bool, int, error) {
	code, dead := m.dead[id]
	return dead, code, nil
}

func (m *mockBackend) Kill(id string) error {
	m.killed = append(m.killed, id)
	m.alive[id] = false
	return nil
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedSession(t *testing.T, store *Store, id string, status Status, lastActive time.Time, logPath string) *AgentSession {
	t.Helper()
	sess := &AgentSession{
		ID: id, Name: id, Harness: "custom", Command: "true", WorkDir: "/",
		Status: status, LogPath: logPath,
		StartedAt: lastActive, LastActive: lastActive,
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestRenderCommand(t *testing.T) {
	set := NewHarnessSet(nil)

	// Every agent-CLI built-in renders bare and with a model.
	for harness, binary := range map[string]string{
		"claude-code": "claude", "codex": "codex", "grok": "grok", "opencode": "opencode",
	} {
		h, err := set.Resolve(harness)
		if err != nil {
			t.Fatalf("built-in %s missing: %v", harness, err)
		}
		got, err := RenderCommand(h, AgentDefinition{}, "")
		if err != nil || got != binary {
			t.Fatalf("%s: got %q, %v", harness, got, err)
		}
		got, err = RenderCommand(h, AgentDefinition{Model: "m1"}, "")
		if err != nil || got != binary+" --model m1" {
			t.Fatalf("%s with model: got %q, %v", harness, got, err)
		}
	}

	custom, _ := set.Resolve("custom")
	got, err := RenderCommand(custom, AgentDefinition{Config: map[string]string{"cmd": "echo hi | cat"}}, "")
	if err != nil || got != "echo hi | cat" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Empty render is a validation failure with exit code 1.
	if _, renderErr := RenderCommand(custom, AgentDefinition{Config: map[string]string{}}, ""); ExitCode(renderErr) != ExitRuntime {
		t.Fatalf("expected exit 1 for empty render, got %v", renderErr)
	}

	// With a files dir, claude-code auto-wires its hooks settings file.
	cc, _ := set.Resolve("claude-code")
	got, err = RenderCommand(cc, AgentDefinition{Model: "opus"}, "/files")
	if err != nil || got != "claude --settings /files/settings.json --model opus" {
		t.Fatalf("claude-code with files dir: got %q, %v", got, err)
	}
}

func TestOpenMaterializesHarnessFiles(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	dd := t.TempDir()
	set := NewHarnessSet(map[string]Harness{
		"hooked": {
			CommandTmpl: `run-agent --config {{.FilesDir}}/agent.toml`,
			Files:       map[string]string{"agent.toml": "notify = true\n"},
		},
	})
	m := &Manager{Store: store, Backend: backend, Harnesses: set, DataDir: dd, IdleThreshold: 5 * time.Second}

	// Any harness — user-defined included — gets its files written; the
	// mechanism carries no harness-specific code.
	sess, err := m.Open(OpenRequest{Harness: "hooked", WorkDir: "/"})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(dd, "harnesses", "hooked")
	if sess.Command != "run-agent --config "+wantDir+"/agent.toml" {
		t.Fatalf("command %q must reference the files dir", sess.Command)
	}
	raw, err := os.ReadFile(filepath.Join(wantDir, "agent.toml"))
	if err != nil || string(raw) != "notify = true\n" {
		t.Fatalf("harness file not materialized: %v %q", err, raw)
	}

	// The builtin claude-code hook wiring rides the same mechanism, as data.
	cc, err := m.Open(OpenRequest{Harness: "claude-code", WorkDir: "/"})
	if err != nil {
		t.Fatal(err)
	}
	ccDir := filepath.Join(dd, "harnesses", "claude-code")
	if !strings.Contains(cc.Command, "--settings "+ccDir+"/settings.json") {
		t.Fatalf("claude-code command %q must load the materialized settings", cc.Command)
	}
	raw, err = os.ReadFile(filepath.Join(ccDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("settings.json must be valid JSON: %v", err)
	}
	if !strings.Contains(string(raw), "awaiting-input") {
		t.Fatal("hooks must call af signal awaiting-input")
	}

	// File names that escape the harness dir are rejected.
	m.Harnesses = NewHarnessSet(map[string]Harness{
		"evil": {CommandTmpl: "x", Files: map[string]string{"../oops": "boom"}},
	})
	if _, err := m.Open(OpenRequest{Harness: "evil", WorkDir: "/"}); ExitCode(err) != ExitRuntime {
		t.Fatalf("path-escaping file name must fail, got %v", err)
	}
}

func TestDetectUniversalFallback(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "uni001", StatusIdle, time.Now().UTC().Add(-time.Hour), "/nonexistent")
	backend.alive[sess.ID] = true
	backend.captured = "Overwrite existing file? (y/n)\n"
	// No harness-specific rules for "custom": the universal set applies.
	detect := CompileDetect(NewHarnessSet(nil), func(w string) { t.Fatalf("unexpected warning: %s", w) })

	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusAwaitingInput || got.StatusOrigin != OriginDetect {
		t.Fatalf("universal rules must cover rule-less harnesses, got %s/%q", got.Status, got.StatusOrigin)
	}

	// The universal working guard suppresses just the same.
	backend.captured = "⠹ crunching…\nOverwrite existing file? (y/n)\n"
	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(sess.ID); got.Status != StatusIdle {
		t.Fatalf("universal working guard must suppress, got %s", got.Status)
	}
}

func TestScanStreamEvents(t *testing.T) {
	all := CompileSignals(nil, func(w string) { t.Fatalf("unexpected warning: %s", w) })

	cases := []struct {
		name    string
		in      string
		want    SignalVerdict
		notices int
	}{
		{"standalone bell", "hello\aworld", SignalAttention, 0},
		{"osc9 notification", "\x1b]9;needs your permission\x07", SignalAttention, 1},
		{"osc777 notification", "\x1b]777;notify;Claude;waiting for input\x1b\\", SignalAttention, 1},
		{"prompt input mark", "\x1b]133;B\x07", SignalAttention, 0},
		{"turn done mark", "\x1b]133;D;0\x07", SignalAttention, 0},
		{"command start mark", "\x1b]133;C\x07", SignalWorking, 0},
		{"prompt start mark ignored", "\x1b]133;A\x07", SignalNone, 0},
		{"ordering: bell then work", "\a...\x1b]133;C\x07", SignalWorking, 0},
		{"ordering: work then bell", "\x1b]133;C\x07...\a", SignalAttention, 0},
		{"osc terminator is not a bell", "\x1b]0;title\x07plain text", SignalNone, 0},
		{"truncated osc ends scan", "text\x1b]9;unfinished", SignalNone, 0},
		{"plain text", "just output\n", SignalNone, 0},
	}
	for _, tc := range cases {
		got := ScanStreamEvents([]byte(tc.in), all)
		if got.Verdict != tc.want || len(got.Notifications) != tc.notices {
			t.Fatalf("%s: got %v/%d notifications, want %v/%d",
				tc.name, got.Verdict, len(got.Notifications), tc.want, tc.notices)
		}
	}

	// A keyword filter narrows which notifications count as attention;
	// non-matching payloads are still recorded.
	filtered := CompileSignals([]string{`(?i)permission`}, func(string) {})
	got := ScanStreamEvents([]byte("\x1b]9;build 50% complete\x07"), filtered)
	if got.Verdict != SignalNone || len(got.Notifications) != 1 {
		t.Fatalf("filtered notification: got %v/%d", got.Verdict, len(got.Notifications))
	}
	if got := ScanStreamEvents([]byte("\x1b]9;needs permission\x07"), filtered); got.Verdict != SignalAttention {
		t.Fatalf("matching notification must be attention, got %v", got.Verdict)
	}

	// Bad patterns warn and are skipped.
	var warnings []string
	bad := CompileSignals([]string{`[unclosed`, `ok`}, func(w string) { warnings = append(warnings, w) })
	if len(warnings) != 1 || len(bad.NotifyAwaiting) != 1 {
		t.Fatalf("want 1 warning + 1 surviving pattern, got %v / %d", warnings, len(bad.NotifyAwaiting))
	}
}

func TestReconcileTermSignals(t *testing.T) {
	sigs := CompileSignals(nil, func(w string) { t.Fatalf("unexpected warning: %s", w) })
	newSess := func(t *testing.T) (*Store, *mockBackend, *AgentSession, string) {
		store, backend := testStore(t), newMockBackend()
		logPath := filepath.Join(t.TempDir(), "s.log")
		os.WriteFile(logPath, []byte{}, 0o644)
		// LastActive = now: the session is NOT quiet, proving signals
		// bypass the idle-threshold gate.
		sess := seedSession(t, store, "trm001", StatusWorking, time.Now().UTC(), logPath)
		backend.alive[sess.ID] = true
		return store, backend, sess, logPath
	}
	pass := func(t *testing.T, store *Store, backend *mockBackend) {
		t.Helper()
		if err := Reconcile(store, backend, 5*time.Second, nil, sigs, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	// Bell at turn end => awaiting-input immediately, no threshold wait.
	store, backend, sess, logPath := newSess(t)
	appendFile(t, logPath, "here is my answer\a")
	pass(t, store, backend)
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusAwaitingInput || got.StatusOrigin != OriginTerm {
		t.Fatalf("bell must set awaiting-input/term immediately, got %s/%q", got.Status, got.StatusOrigin)
	}

	// Term-set state persists through a quiet pass...
	pass(t, store, backend)
	if got, _ := store.GetSession(sess.ID); got.Status != StatusAwaitingInput {
		t.Fatalf("term-set state must persist while quiet, got %s", got.Status)
	}
	// ...a command-start mark clears it to working...
	appendFile(t, logPath, "\x1b]133;C\x07")
	pass(t, store, backend)
	if got, _ := store.GetSession(sess.ID); got.Status != StatusWorking || got.StatusOrigin != "" {
		t.Fatalf("cmd-start must clear term state, got %s/%q", got.Status, got.StatusOrigin)
	}
	// ...and a notification re-raises it.
	appendFile(t, logPath, "\x1b]9;agent needs your permission\x07")
	pass(t, store, backend)
	if got, _ := store.GetSession(sess.ID); got.Status != StatusAwaitingInput || got.StatusOrigin != OriginTerm {
		t.Fatalf("notification must set awaiting-input/term, got %s/%q", got.Status, got.StatusOrigin)
	}
	// Plain meaningful output clears it back to working.
	appendFile(t, logPath, "user replied, working\n")
	pass(t, store, backend)
	if got, _ := store.GetSession(sess.ID); got.Status != StatusWorking {
		t.Fatalf("output must clear term state, got %s", got.Status)
	}

	// T2 signal outranks T1.75: a bell-only delta never touches
	// signal-set done.
	store, backend, sess, logPath = newSess(t)
	m := &Manager{Store: store, Backend: backend}
	fresh, _ := store.GetSession(sess.ID)
	if err := m.Signal(fresh, StatusDone); err != nil {
		t.Fatal(err)
	}
	appendFile(t, logPath, "\a")
	pass(t, store, backend)
	if got, _ := store.GetSession(sess.ID); got.Status != StatusDone || got.StatusOrigin != OriginSignal {
		t.Fatalf("signal-set done must outrank a bell, got %s/%q", got.Status, got.StatusOrigin)
	}
}

func TestHarnessSetResolve(t *testing.T) {
	set := NewHarnessSet(map[string]Harness{"opencode": {CommandTmpl: "opencode"}})
	if _, err := set.Resolve("opencode"); err != nil {
		t.Fatal(err)
	}
	_, err := set.Resolve("nope")
	if ExitCode(err) != ExitRuntime {
		t.Fatalf("expected exit 1, got %v", err)
	}
	for _, known := range []string{"claude-code", "custom", "opencode"} {
		if !regexp.MustCompile(known).MatchString(err.Error()) {
			t.Errorf("error should list %q: %v", known, err)
		}
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[a-z0-9]{6}$`).MatchString(id) {
			t.Fatalf("bad id %q", id)
		}
		seen[id] = true
	}
	if len(seen) < 95 { // collisions in 100 draws over 36^6 would be a bug
		t.Fatalf("suspiciously many collisions: %d unique", len(seen))
	}
}

func TestSuffixName(t *testing.T) {
	taken := map[string]bool{"planner": true, "planner-2": true}
	fn := func(s string) bool { return taken[s] }
	if got := SuffixName("planner", fn); got != "planner-3" {
		t.Fatalf("got %q", got)
	}
	if got := SuffixName("fresh", fn); got != "fresh" {
		t.Fatalf("got %q", got)
	}
}

func TestMergeEnvPrecedence(t *testing.T) {
	got := MergeEnv(
		map[string]string{"A": "harness", "B": "harness"},
		map[string]string{"B": "def", "C": "def"},
		map[string]string{"C": "flag"},
	)
	want := map[string]string{"A": "harness", "B": "def", "C": "flag"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// --- reconciliation transitions (§7.1 / §10.1) ---

func TestReconcileMissingSessionFails(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	seedSession(t, store, "aaaaaa", StatusWorking, time.Now().UTC(), "/nonexistent")
	// backend.alive["aaaaaa"] is false: tmux session missing.
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession("aaaaaa")
	if got.Status != StatusFailed || got.EndedAt == nil || got.ExitCode != nil {
		t.Fatalf("want failed with EndedAt and nil ExitCode, got %+v", got)
	}
}

func TestReconcileDeadPaneExits(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	seedSession(t, store, "bbbbbb", StatusWorking, time.Now().UTC(), "/nonexistent")
	backend.alive["bbbbbb"] = true
	backend.dead["bbbbbb"] = 7
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession("bbbbbb")
	if got.Status != StatusExited || got.ExitCode == nil || *got.ExitCode != 7 || got.EndedAt == nil {
		t.Fatalf("want exited(7), got %+v", got)
	}
	if len(backend.killed) != 1 || backend.killed[0] != "bbbbbb" {
		t.Fatalf("kill-session not called: %v", backend.killed)
	}
}

func TestReconcileLogGrowthActivates(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	logPath := filepath.Join(t.TempDir(), "s.log")
	os.WriteFile(logPath, []byte("output!"), 0o644)
	old := time.Now().UTC().Add(-time.Hour)
	sess := seedSession(t, store, "cccccc", StatusAwaitingInput, old, logPath)
	backend.alive[sess.ID] = true
	now := time.Now().UTC().Truncate(time.Second)
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusWorking {
		t.Fatalf("log growth must clear awaiting-input to working, got %s", got.Status)
	}
	if !got.LastActive.Equal(now) {
		t.Fatalf("LastActive not updated: %v != %v", got.LastActive, now)
	}
	if got.LogOffset != int64(len("output!")) {
		t.Fatalf("high-water mark not stored: %d", got.LogOffset)
	}
}

// animationFrame mimics grok-cli's idle braille-logo redraw: cursor
// addressing + truecolor + braille glyphs, no letters or digits in the
// rendered text.
func animationFrame() string {
	return "\x1b[?2026h\x1b[17;15H\x1b[38;2;145;145;145;48;2;20;20;20m⠀⠁" +
		"\x1b[18;15H\x1b[38;2;150;150;150m⣠⣾⣿\x1b[19;15H⣼⡿▁▔█\x1b[?2026l"
}

func TestMeaningfulText(t *testing.T) {
	if MeaningfulText([]byte(animationFrame() + animationFrame())) {
		t.Error("braille animation frames must not count as meaningful")
	}
	if MeaningfulText([]byte("\x1b[2K\x1b[1G> ❯ │ ─ ")) {
		t.Error("prompt/border symbols must not count as meaningful")
	}
	if !MeaningfulText([]byte(animationFrame() + "\x1b[5;1HSure, here's the plan")) {
		t.Error("real words must count as meaningful")
	}
	if !MeaningfulText([]byte("\x1b[31m42\x1b[0m")) {
		t.Error("digits must count as meaningful")
	}
}

func TestReconcileIgnoresAnimationOnlyGrowth(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	logPath := filepath.Join(t.TempDir(), "s.log")
	os.WriteFile(logPath, []byte("grok banner\n"), 0o644)
	// Session already saw the banner (offset at EOF), then went quiet
	// except for the idle animation.
	sess := seedSession(t, store, "anim01", StatusWorking, time.Now().UTC().Add(-time.Hour), logPath)
	sess.LogOffset = int64(len("grok banner\n"))
	if err := store.UpdateSession(sess); err != nil {
		t.Fatal(err)
	}
	backend.alive[sess.ID] = true

	appendFile(t, logPath, animationFrame()+animationFrame())
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusIdle {
		t.Fatalf("animation-only growth must go idle, got %s", got.Status)
	}
	if got.LogOffset <= sess.LogOffset {
		t.Fatal("offset must advance past animation bytes")
	}

	// awaiting-input survives animation frames...
	got.Status = StatusAwaitingInput
	if err := store.UpdateSession(got); err != nil {
		t.Fatal(err)
	}
	appendFile(t, logPath, animationFrame())
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.GetSession(sess.ID); got.Status != StatusAwaitingInput {
		t.Fatalf("animation must not clear awaiting-input, got %s", got.Status)
	}

	// ...but real output clears it.
	appendFile(t, logPath, animationFrame()+"Done! Anything else?\n")
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.GetSession(sess.ID); got.Status != StatusWorking {
		t.Fatalf("real output must clear awaiting-input to working, got %s", got.Status)
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAwaitingInputPersistsWithoutGrowth(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "dddddd", StatusAwaitingInput, time.Now().UTC().Add(-time.Hour), "/nonexistent")
	backend.alive[sess.ID] = true
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusAwaitingInput {
		t.Fatalf("awaiting-input must persist without growth, got %s", got.Status)
	}
}

func TestReconcileIdleThreshold(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	now := time.Now().UTC()

	stale := seedSession(t, store, "eeeeee", StatusWorking, now.Add(-10*time.Second), "/nonexistent")
	fresh := seedSession(t, store, "ffffff", StatusStarting, now.Add(-time.Second), "/nonexistent")
	backend.alive[stale.ID] = true
	backend.alive[fresh.ID] = true

	if err := Reconcile(store, backend, 5*time.Second, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(stale.ID); got.Status != StatusIdle {
		t.Fatalf("stale session should be idle, got %s", got.Status)
	}
	// §7.1 rule 1: starting -> working only on first observed growth, so
	// a quiet fresh session stays starting within the threshold...
	if got, _ := store.GetSession(fresh.ID); got.Status != StatusStarting {
		t.Fatalf("quiet fresh session should stay starting, got %s", got.Status)
	}
	// ...and reads idle once the threshold passes without any output.
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(fresh.ID); got.Status != StatusIdle {
		t.Fatalf("quiet starting session should go idle after threshold, got %s", got.Status)
	}
}

func TestReconcileStartingToActiveOnGrowth(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	logPath := filepath.Join(t.TempDir(), "s.log")
	os.WriteFile(logPath, []byte{}, 0o644)
	sess := seedSession(t, store, "gggggg", StatusStarting, time.Now().UTC(), logPath)
	backend.alive[sess.ID] = true

	appendFile(t, logPath, "hello from the harness\n")
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(sess.ID); got.Status != StatusWorking {
		t.Fatalf("starting session with output should be working, got %s", got.Status)
	}
}

func TestOpenValidation(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	m := &Manager{
		Store: store, Backend: backend, Harnesses: NewHarnessSet(nil),
		DataDir: t.TempDir(), IdleThreshold: 5 * time.Second,
	}

	// No harness at all: usage error.
	_, err := m.Open(OpenRequest{})
	if ExitCode(err) != ExitUsage {
		t.Fatalf("want exit 2, got %v", err)
	}
	// Unknown harness: runtime error listing known ones.
	_, err = m.Open(OpenRequest{Harness: "nope"})
	if ExitCode(err) != ExitRuntime {
		t.Fatalf("want exit 1, got %v", err)
	}
	// Unknown definition: not found.
	_, err = m.Open(OpenRequest{Definition: "ghost"})
	if ExitCode(err) != ExitNotFound {
		t.Fatalf("want exit 3, got %v", err)
	}
	// Missing workdir: runtime error.
	_, err = m.Open(OpenRequest{Cmd: "true", WorkDir: "/definitely/not/here"})
	if ExitCode(err) != ExitRuntime {
		t.Fatalf("want exit 1, got %v", err)
	}
}

func TestOpenAdHocAndNameSuffix(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	m := &Manager{
		Store: store, Backend: backend, Harnesses: NewHarnessSet(nil),
		DataDir: t.TempDir(), IdleThreshold: 5 * time.Second,
	}

	first, err := m.Open(OpenRequest{Cmd: "sleep 1", Name: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Harness != "custom" || first.Command != "sleep 1" || first.Status != StatusStarting {
		t.Fatalf("unexpected session: %+v", first)
	}
	if first.Env["AF_SESSION_ID"] != first.ID || first.Env["AF_SESSION_NAME"] != "worker" {
		t.Fatalf("AF_* env not injected: %v", first.Env)
	}
	second, err := m.Open(OpenRequest{Cmd: "sleep 1", Name: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Name != "worker-2" {
		t.Fatalf("expected suffixed name, got %q", second.Name)
	}
}

func TestSignalStates(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "sig001", StatusIdle, time.Now().UTC(), "/nonexistent")
	backend.alive[sess.ID] = true
	m := &Manager{Store: store, Backend: backend}

	if err := m.Signal(sess, StatusDone); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusDone || got.StatusOrigin != OriginSignal {
		t.Fatalf("got %s/%q, want done/signal", got.Status, got.StatusOrigin)
	}
	// Only the sticky harness-reported states are valid signals.
	if err := m.Signal(sess, Status("busy")); ExitCode(err) != ExitUsage {
		t.Fatalf("unknown state: want exit 2, got %v", err)
	}
	if err := m.Signal(sess, StatusWorking); ExitCode(err) != ExitUsage {
		t.Fatalf("non-sticky state: want exit 2, got %v", err)
	}
}

func TestReconcileDonePersistsAndClears(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	logPath := filepath.Join(t.TempDir(), "s.log")
	os.WriteFile(logPath, []byte{}, 0o644)
	sess := seedSession(t, store, "don001", StatusIdle, time.Now().UTC().Add(-time.Hour), logPath)
	backend.alive[sess.ID] = true
	m := &Manager{Store: store, Backend: backend}
	if err := m.Signal(sess, StatusDone); err != nil {
		t.Fatal(err)
	}

	// Quiet pass: done persists (it is sticky, like awaiting-input).
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(sess.ID); got.Status != StatusDone {
		t.Fatalf("done must persist without growth, got %s", got.Status)
	}

	// Meaningful output clears it back to working and clears the origin.
	appendFile(t, logPath, "picked up a new task\n")
	if err := Reconcile(store, backend, 5*time.Second, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusWorking || got.StatusOrigin != "" {
		t.Fatalf("output must clear done, got %s/%q", got.Status, got.StatusOrigin)
	}
}

func TestWaitOutcomes(t *testing.T) {
	newManager := func(t *testing.T) (*Manager, *mockBackend, *AgentSession) {
		store, backend := testStore(t), newMockBackend()
		sess := seedSession(t, store, "wai001", StatusWorking, time.Now().UTC().Add(-time.Hour), "/nonexistent")
		backend.alive[sess.ID] = true
		return &Manager{Store: store, Backend: backend, IdleThreshold: 5 * time.Second}, backend, sess
	}

	// Reached: the stale session reconciles to idle on the first poll.
	m, _, sess := newManager(t)
	got, outcome, err := m.Wait(sess.ID, map[Status]bool{StatusIdle: true}, time.Second, time.Millisecond)
	if err != nil || outcome != WaitReached || got.Status != StatusIdle {
		t.Fatalf("want reached/idle, got %v/%v/%v", outcome, got, err)
	}

	// Terminal status outside the target set.
	m, backend, sess := newManager(t)
	backend.dead[sess.ID] = 3
	got, outcome, err = m.Wait(sess.ID, map[Status]bool{StatusDone: true}, time.Second, time.Millisecond)
	if err != nil || outcome != WaitTerminal || got.Status != StatusExited {
		t.Fatalf("want terminal/exited, got %v/%v/%v", outcome, got, err)
	}

	// Terminal status inside the target set counts as reached.
	m, backend, sess = newManager(t)
	backend.dead[sess.ID] = 0
	_, outcome, err = m.Wait(sess.ID, map[Status]bool{StatusExited: true}, time.Second, time.Millisecond)
	if err != nil || outcome != WaitReached {
		t.Fatalf("want reached for targeted terminal, got %v/%v", outcome, err)
	}

	// Timeout: the session never reaches done.
	m, _, sess = newManager(t)
	_, outcome, err = m.Wait(sess.ID, map[Status]bool{StatusDone: true}, 150*time.Millisecond, time.Millisecond)
	if err != nil || outcome != WaitTimeout {
		t.Fatalf("want timeout, got %v/%v", outcome, err)
	}
}

func TestParseStatus(t *testing.T) {
	if s, err := ParseStatus("awaiting-input"); err != nil || s != StatusAwaitingInput {
		t.Fatalf("got %v/%v", s, err)
	}
	if _, err := ParseStatus("thinking"); ExitCode(err) != ExitUsage {
		t.Fatalf("unknown status: want exit 2, got %v", err)
	}
}

// detectSet compiles rules for the test harness name used by seedSession.
func detectSet(t *testing.T, rules DetectRules) map[string]*CompiledDetect {
	t.Helper()
	set := NewHarnessSet(map[string]Harness{"custom": {CommandTmpl: "x", Detect: rules}})
	var warnings []string
	compiled := CompileDetect(set, func(w string) { warnings = append(warnings, w) })
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	return compiled
}

func TestDetectSetsAwaitingInput(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "det001", StatusIdle, time.Now().UTC().Add(-time.Hour), "/nonexistent")
	backend.alive[sess.ID] = true
	backend.captured = "│ Do you want to proceed?\n│ ❯ 1. Yes\n"
	detect := detectSet(t, DetectRules{AwaitingInput: []string{`(?i)do you want`}, Working: []string{`esc to interrupt`}})

	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusAwaitingInput || got.StatusOrigin != OriginDetect {
		t.Fatalf("want awaiting-input/detect, got %s/%q", got.Status, got.StatusOrigin)
	}

	// Screen stops matching => detect-set state reverts to idle.
	backend.captured = "some scrollback\n"
	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetSession(sess.ID)
	if got.Status != StatusIdle || got.StatusOrigin != "" {
		t.Fatalf("want idle after unmatch, got %s/%q", got.Status, got.StatusOrigin)
	}
}

func TestDetectWorkingGuardSuppresses(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "det002", StatusIdle, time.Now().UTC().Add(-time.Hour), "/nonexistent")
	backend.alive[sess.ID] = true
	backend.captured = "Do you want tea? … (esc to interrupt)\n"
	detect := detectSet(t, DetectRules{AwaitingInput: []string{`(?i)do you want`}, Working: []string{`esc to interrupt`}})

	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(sess.ID); got.Status != StatusIdle {
		t.Fatalf("working guard must suppress detection, got %s", got.Status)
	}
}

func TestDetectNeverOverridesSignal(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	sess := seedSession(t, store, "det003", StatusIdle, time.Now().UTC().Add(-time.Hour), "/nonexistent")
	backend.alive[sess.ID] = true
	m := &Manager{Store: store, Backend: backend}
	if err := m.Signal(sess, StatusDone); err != nil {
		t.Fatal(err)
	}
	backend.captured = "Do you want to proceed?\n"
	detect := detectSet(t, DetectRules{AwaitingInput: []string{`(?i)do you want`}})

	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(sess.ID)
	if got.Status != StatusDone || got.StatusOrigin != OriginSignal {
		t.Fatalf("detection must not override a signal, got %s/%q", got.Status, got.StatusOrigin)
	}
}

func TestDetectSkipsActiveSessions(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	// Fresh activity: within the idle threshold, so detection must not run.
	sess := seedSession(t, store, "det004", StatusWorking, time.Now().UTC(), "/nonexistent")
	backend.alive[sess.ID] = true
	backend.captured = "Do you want to proceed?\n"
	detect := detectSet(t, DetectRules{AwaitingInput: []string{`(?i)do you want`}})

	if err := Reconcile(store, backend, 5*time.Second, detect, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSession(sess.ID); got.Status != StatusWorking {
		t.Fatalf("working session must skip detection, got %s", got.Status)
	}
}

func TestCompileDetectWarnsOnBadRegex(t *testing.T) {
	set := NewHarnessSet(map[string]Harness{
		"h": {CommandTmpl: "x", Detect: DetectRules{AwaitingInput: []string{`[unclosed`, `ok`}}},
	})
	var warnings []string
	compiled := CompileDetect(set, func(w string) { warnings = append(warnings, w) })
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the bad regex, got %v", warnings)
	}
	if compiled["h"] == nil || len(compiled["h"].AwaitingInput) != 1 {
		t.Fatalf("good pattern must survive: %+v", compiled["h"])
	}
	// claude-code ships built-in patterns.
	if compiled["claude-code"] == nil || len(compiled["claude-code"].AwaitingInput) == 0 {
		t.Fatal("claude-code must have built-in detect patterns")
	}
	// Harnesses without rules compile to nothing (no capture cost).
	if compiled["grok"] != nil {
		t.Fatalf("grok has no rules, got %+v", compiled["grok"])
	}
}

func TestHarnessBinary(t *testing.T) {
	set := NewHarnessSet(map[string]Harness{
		"myagent": {CommandTmpl: `myagent --config {{.FilesDir}}/x`},
	})
	for name, want := range map[string]string{
		"claude-code": "claude", "codex": "codex", "grok": "grok",
		"opencode": "opencode", "custom": "", "myagent": "myagent",
	} {
		h, _ := set.Resolve(name)
		if got := h.Binary(); got != want {
			t.Errorf("%s.Binary() = %q, want %q", name, got, want)
		}
	}
}

func TestMatchBinary(t *testing.T) {
	set := NewHarnessSet(nil)
	if h, ok := set.MatchBinary("claude"); !ok || h.Name != "claude-code" {
		t.Fatalf("claude -> %v/%v, want claude-code", h.Name, ok)
	}
	if h, ok := set.MatchBinary("agent"); !ok || h.Name != "agent" {
		t.Fatalf("agent -> %v/%v, want agent", h.Name, ok)
	}
	if h, ok := set.MatchBinary("cursor-agent"); !ok || h.Name != "cursor-agent" {
		t.Fatalf("cursor-agent -> %v/%v, want cursor-agent", h.Name, ok)
	}
	if _, ok := set.MatchBinary("nope"); ok {
		t.Fatal("nope must not match")
	}
	if _, ok := set.MatchBinary(""); ok {
		t.Fatal("empty (custom's binary) must not match")
	}
}

func TestSeedFromSession(t *testing.T) {
	sess := &AgentSession{
		Name: "claude-proj", Harness: "claude-code", Model: "opus",
		WorkDir: "/home/x/proj", Service: true, Command: "claude --model opus",
		Env: map[string]string{"FOO": "bar", EnvSessionID: "abc123", EnvSessionName: "claude-proj"},
	}
	def := &AgentDefinition{Name: "keepme"}
	def.SeedFromSession(sess)

	if def.Name != "keepme" {
		t.Fatalf("name must be preserved, got %q", def.Name)
	}
	if def.Harness != "claude-code" || def.Model != "opus" || def.WorkDir != "/home/x/proj" || !def.Service {
		t.Fatalf("launch fields not copied: %+v", def)
	}
	if def.Env["FOO"] != "bar" {
		t.Fatalf("user env not carried over: %v", def.Env)
	}
	if _, ok := def.Env[EnvSessionID]; ok {
		t.Fatal("per-session AF_SESSION_ID must be dropped")
	}
	if _, ok := def.Env[EnvSessionName]; ok {
		t.Fatal("per-session AF_SESSION_NAME must be dropped")
	}
	if def.Config["cmd"] != "" {
		t.Fatalf("non-custom harness must not capture a command, got %q", def.Config["cmd"])
	}

	// A custom session captures its exact command as config cmd.
	cd := &AgentDefinition{Name: "c"}
	cd.SeedFromSession(&AgentSession{Harness: "custom", Command: "sleep 300"})
	if cd.Harness != "custom" || cd.Config["cmd"] != "sleep 300" {
		t.Fatalf("custom command not captured: %+v", cd)
	}
}

func TestQuoteArgs(t *testing.T) {
	if got := QuoteArgs([]string{"--model", "opus", "write a poem"}); got != `'--model' 'opus' 'write a poem'` {
		t.Fatalf("got %q", got)
	}
	if got := QuoteArgs([]string{"it's"}); got != `'it'\''s'` {
		t.Fatalf("quote-escaping: got %q", got)
	}
}

func TestOpenExtraArgs(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	m := &Manager{
		Store: store, Backend: backend, Harnesses: NewHarnessSet(nil),
		DataDir: t.TempDir(), IdleThreshold: 5 * time.Second,
	}
	sess, err := m.Open(OpenRequest{Cmd: "sleep 600", WorkDir: "/", ExtraArgs: []string{"--foo", "a b"}})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Command != `sleep 600 '--foo' 'a b'` {
		t.Fatalf("passthrough not appended: %q", sess.Command)
	}
}

func TestLiveMatch(t *testing.T) {
	store, backend := testStore(t), newMockBackend()
	m := &Manager{Store: store, Backend: backend}
	now := time.Now().UTC()
	seed := func(id, harness, wd string, status Status, last time.Time, service bool) {
		s := &AgentSession{
			ID: id, Name: id, Harness: harness, Command: "x", WorkDir: wd,
			Status: status, Service: service, StartedAt: last, LastActive: last, LogPath: "/x",
		}
		if err := store.InsertSession(s); err != nil {
			t.Fatal(err)
		}
	}
	seed("old", "claude-code", "/repo", StatusIdle, now.Add(-time.Hour), false)
	seed("new", "claude-code", "/repo", StatusWorking, now, false)     // most recent -> winner
	seed("other", "claude-code", "/elsewhere", StatusIdle, now, false) // wrong workdir
	seed("codex", "codex", "/repo", StatusIdle, now, false)            // wrong harness
	seed("svc", "claude-code", "/repo", StatusWorking, now, true)      // service, ignored
	seed("dead", "claude-code", "/repo", StatusExited, now, false)     // terminal, excluded by ListSessions

	m2, err := m.LiveMatch("claude-code", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if m2 == nil || m2.ID != "new" {
		t.Fatalf("want most-recent live match 'new', got %v", m2)
	}
	if m3, _ := m.LiveMatch("grok", "/repo"); m3 != nil {
		t.Fatalf("no grok session; want nil, got %v", m3)
	}
}
