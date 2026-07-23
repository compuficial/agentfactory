package cli

import (
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		raw    bool
	)
	c := &cobra.Command{
		Use:   "logs <session>",
		Short: "Print a session's captured output (cleaned of terminal escapes; --raw for bytes)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			var data []byte
			if raw {
				data, err = os.ReadFile(sess.LogPath)
				if err != nil && !os.IsNotExist(err) {
					return core.Errf(core.ExitRuntime, "read log: %v", err)
				}
			} else {
				cleaned, err := core.ReadLogTail(sess.LogPath, 0)
				if err != nil {
					return core.Errf(core.ExitRuntime, "read log: %v", err)
				}
				data = []byte(cleaned)
			}
			out := cmd.OutOrStdout()
			if _, err := out.Write(core.TailLines(data, lines)); err != nil {
				return err
			}
			if !follow {
				return nil
			}
			// Follow from the current raw end of file.
			offset := int64(0)
			if fi, err := os.Stat(sess.LogPath); err == nil {
				offset = fi.Size()
			}
			return followFile(out, sess.LogPath, offset, raw)
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as output arrives")
	c.Flags().IntVarP(&lines, "lines", "n", 200, "lines to print (0 = whole file)")
	c.Flags().BoolVar(&raw, "raw", false, "print the raw byte stream (escape sequences included)")
	return c
}

// followFile polls the log for growth (tail -f semantics; runs until
// the process is interrupted).
func followFile(out io.Writer, path string, offset int64, raw bool) error {
	for {
		time.Sleep(200 * time.Millisecond)
		fi, err := os.Stat(path)
		if err != nil || fi.Size() <= offset {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err == nil {
			chunk, err := io.ReadAll(f)
			if err == nil && len(chunk) > 0 {
				offset += int64(len(chunk))
				if raw {
					out.Write(chunk)
				} else {
					io.WriteString(out, core.SanitizeTerminal(chunk))
				}
			}
		}
		f.Close()
	}
}
