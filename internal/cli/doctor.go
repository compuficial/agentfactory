package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
	"agentfactory.sh/af/internal/tmux"
)

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

func newDoctorCmd() *cobra.Command {
	var jsonMode bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check the af environment (tmux, socket, database, dirs)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			report := runDoctorChecks(cfg)
			if jsonMode {
				if err := writeJSON(cmd, report); err != nil {
					return err
				}
			} else {
				for _, ch := range report.Checks {
					mark := "ok"
					if !ch.OK {
						mark = "FAIL"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[%s]\t%s: %s\n", mark, ch.Name, ch.Detail)
				}
			}
			if !report.OK {
				return core.Errf(core.ExitEnv, "environment checks failed")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonMode, "json", false, "machine-readable output")
	return c
}

// runDoctorChecks probes the environment (tmux, socket, dirs, DB,
// reconciliation, config) and collects the results into one report.
func runDoctorChecks(cfg *config.Config) doctorReport {
	report := doctorReport{OK: true}
	add := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, OK: ok, Detail: detail})
		report.OK = report.OK && ok
	}

	// tmux present and >= 3.2
	version, err := tmux.CheckTmux()
	if err != nil {
		add("tmux", false, err.Error())
	} else {
		add("tmux", true, "tmux "+version)
	}

	// af socket server reachable or creatable
	backend := tmux.New(cfg.Socket, cfg.SendDelay.D())
	if err == nil { // only meaningful with a working tmux
		if serr := backend.EnsureServer(); serr != nil {
			add("socket", false, serr.Error())
		} else {
			add("socket", true, fmt.Sprintf("tmux server on socket %q", cfg.Socket))
		}
	} else {
		add("socket", false, "skipped: tmux unavailable")
	}

	// data + log dirs writable
	logDir := filepath.Join(cfg.DataDir, "logs")
	if derr := writableDir(logDir); derr != nil {
		add("data_dir", false, derr.Error())
	} else {
		add("data_dir", true, cfg.DataDir+" writable")
	}

	// DB opens and schema version matches
	store, serr := core.OpenStore(cfg.DataDir)
	if serr != nil {
		add("database", false, serr.Error())
	} else {
		defer store.Close()
		addDatabaseCheck(add, store, cfg.DataDir)
	}

	// reconciliation dry-run report (needs tmux and the DB)
	if store != nil && err == nil {
		detail, ok := dryRunReport(store, backend, cfg.DataDir)
		add("reconciliation", ok, detail)
	} else {
		add("reconciliation", false, "skipped: needs tmux and database")
	}

	// fully resolved config (§9)
	add("config", true, "resolved:\n"+cfg.Resolved())
	return report
}

// addDatabaseCheck reports whether the schema version matches this build.
func addDatabaseCheck(add func(string, bool, string), store *core.Store, dataDir string) {
	dbPath := filepath.Join(dataDir, "agentfactory.db")
	if have, want, verr := store.SchemaVersion(); verr != nil {
		add("database", false, dbPath+": read schema version: "+verr.Error())
	} else if have != want {
		// Shape-compatible but versioned by another af build
		// sharing this data dir: report, don't fail.
		add("database", true, fmt.Sprintf("%s (schema version %d, this build writes %d — shape-compatible)", dbPath, have, want))
	} else {
		add("database", true, fmt.Sprintf("%s (schema version %d)", dbPath, have))
	}
}

// Probe permissions: same-user data dirs and a throwaway probe file.
const (
	probeDirPerm  = 0o750
	probeFilePerm = 0o600
)

func writableDir(dir string) error {
	if err := os.MkdirAll(dir, probeDirPerm); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".af-doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), probeFilePerm); err != nil {
		return err
	}
	return os.Remove(probe)
}

// dryRunReport counts what a real reconciliation would change, without
// mutating anything. ok is false when the report itself failed.
func dryRunReport(store *core.Store, backend *tmux.Backend, dataDir string) (string, bool) {
	sessions, err := store.ListSessions(false)
	if err != nil {
		return "error: " + err.Error(), false
	}
	orphaned, pending := 0, 0
	for _, s := range sessions {
		alive, err := backend.IsAlive(s.ID)
		if err != nil {
			return "error: " + err.Error(), false
		}
		if !alive {
			orphaned++
			continue
		}
		if dead, _, err := backend.DeadStatus(s.ID); err == nil && dead {
			pending++
		}
	}
	var logBytes int64
	entries, _ := os.ReadDir(filepath.Join(dataDir, "logs"))
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			logBytes += info.Size()
		}
	}
	return fmt.Sprintf("%d live rows, %d orphaned, %d dead panes pending harvest, %s of logs",
		len(sessions), orphaned, pending, humanBytes(logBytes)), true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
