package cli

import (
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

const (
	// defaultLogLines is how much history `af logs` prints by default.
	defaultLogLines = 200
	// followPoll is the tail -f polling cadence.
	followPoll = 200 * time.Millisecond
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
	c.Flags().IntVarP(&lines, "lines", "n", defaultLogLines, "lines to print (0 = whole file)")
	c.Flags().BoolVar(&raw, "raw", false, "print the raw byte stream (escape sequences included)")
	return c
}

// followFile polls the log for growth (tail -f semantics; runs until
// the process is interrupted or the output stops accepting writes).
func followFile(out io.Writer, path string, offset int64, raw bool) error {
	for {
		time.Sleep(followPoll)
		chunk := readChunk(path, offset)
		if len(chunk) == 0 {
			continue
		}
		offset += int64(len(chunk))
		var writeErr error
		if raw {
			_, writeErr = out.Write(chunk)
		} else {
			_, writeErr = io.WriteString(out, core.SanitizeTerminal(chunk))
		}
		if writeErr != nil {
			return writeErr // output gone (closed pipe); stop following
		}
	}
}

// readChunk returns the log bytes past offset, or nil when the file
// hasn't grown (or is briefly unreadable — the next poll retries).
func readChunk(path string, offset int64) []byte {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= offset {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
		return nil
	}
	chunk, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return chunk
}
