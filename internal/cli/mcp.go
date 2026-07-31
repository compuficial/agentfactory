package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve af operations as MCP tools on stdio",
		Long: `Run a Model Context Protocol server on stdin/stdout. Register it in an
MCP-native harness (e.g. claude mcp add af -- af mcp) and the agent gets
typed tools for opening, steering, and waiting on sibling af sessions.
The server is spawned per client and dies with it: no daemon, no port.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := newMCPServer(func() (*App, error) { return newApp(cmd) })
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}

// Tool argument structs. jsonschema tags become the client-visible
// parameter descriptions.
type mcpStatusArgs struct {
	All bool `json:"all,omitempty" jsonschema:"include exited/failed history"`
}
type mcpSessionArgs struct {
	Session string `json:"session" jsonschema:"session ID or name"`
}
type mcpPeekArgs struct {
	Session string `json:"session" jsonschema:"session ID or name"`
	Lines   int    `json:"lines,omitempty" jsonschema:"trailing screen lines (0 = full visible screen)"`
}
type mcpLogsArgs struct {
	Session string `json:"session" jsonschema:"session ID or name"`
	Lines   int    `json:"lines,omitempty" jsonschema:"trailing log lines (default 200)"`
}
type mcpSendArgs struct {
	Session string `json:"session" jsonschema:"session ID or name"`
	Text    string `json:"text" jsonschema:"input to inject"`
	NoEnter bool   `json:"no_enter,omitempty" jsonschema:"omit the trailing Enter"`
}
type mcpOpenArgs struct {
	Definition string `json:"definition,omitempty" jsonschema:"definition name to instantiate"`
	Name       string `json:"name,omitempty" jsonschema:"session name"`
	Harness    string `json:"harness,omitempty" jsonschema:"harness name (for ad-hoc sessions)"`
	Cmd        string `json:"cmd,omitempty" jsonschema:"raw command (implies the custom harness)"`
	Model      string `json:"model,omitempty" jsonschema:"model passthrough"`
	WorkDir    string `json:"workdir,omitempty" jsonschema:"working directory (default: current)"`
}
type mcpWaitArgs struct {
	Session        string `json:"session" jsonschema:"session ID or name"`
	For            string `json:"for,omitempty" jsonschema:"comma-separated target statuses (default idle,awaiting-input,done)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"default 60, max 600; on timeout the result has timed_out=true — call again to keep waiting"`
}
type mcpSignalArgs struct {
	State   string `json:"state" jsonschema:"awaiting-input or done"`
	Session string `json:"session,omitempty" jsonschema:"session ID or name; defaults to your own session (AF_SESSION_ID)"`
}

// appTool adapts a handler to the lifecycle every tool shares: a fresh
// App per call, closed when the call ends — daemonless, so nothing
// outlives one call and config/DB state is never held stale.
func appTool[In any](app func() (*App, error), fn func(context.Context, *App, In) (*mcp.CallToolResult, any, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		a, err := app()
		if err != nil {
			return nil, nil, err
		}
		defer a.Close()
		return fn(ctx, a, in)
	}
}

// newMCPServer builds the tool surface over the shared App factory.
func newMCPServer(app func() (*App, error)) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "af", Version: Version}, &mcp.ServerOptions{
		Instructions: `af manages sibling agent sessions in tmux. Use af_status to see them,
af_open to start one, af_send + af_peek/af_logs to steer one, af_wait to
block until a peer stops working (idle/awaiting-input/done), and
af_signal with state "done" to report your own task complete so peers
waiting on you unblock.`,
	})
	addStatusTool(server, app)
	addPeekTool(server, app)
	addLogsTool(server, app)
	addSendTool(server, app)
	addOpenTool(server, app)
	addWaitTool(server, app)
	addCloseTool(server, app)
	addDefsTool(server, app)
	addSignalTool(server, app)
	return server
}

func addStatusTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_status",
		Description: "List af sessions with status. Call this first to discover peers.",
	}, appTool(app, func(_ context.Context, a *App, args mcpStatusArgs) (*mcp.CallToolResult, any, error) {
		sessions, err := a.Store.ListSessions(args.All)
		if err != nil {
			return nil, nil, err
		}
		out := []core.SessionJSON{}
		for _, s := range sessions {
			out = append(out, s.JSON())
		}
		return mcpJSON(out)
	}))
}

func addPeekTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_peek",
		Description: "Read a session's current rendered screen. Use after af_wait to see what a peer produced or is asking.",
	}, appTool(app, func(_ context.Context, a *App, args mcpPeekArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.ResolveOne(args.Session)
		if err != nil {
			return nil, nil, err
		}
		screen, err := a.Manager.Backend.CapturePane(sess.ID, args.Lines)
		if err != nil {
			return nil, nil, err
		}
		return mcpText(screen), nil, nil
	}))
}

func addLogsTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_logs",
		Description: "Read a session's captured output history (scrollback survives detach).",
	}, appTool(app, func(_ context.Context, a *App, args mcpLogsArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.ResolveOne(args.Session)
		if err != nil {
			return nil, nil, err
		}
		cleaned, err := core.ReadLogTail(sess.LogPath, logReadBytes)
		if err != nil {
			return nil, nil, core.Errf(core.ExitRuntime, "read log: %v", err)
		}
		lines := args.Lines
		if lines <= 0 {
			lines = defaultLogLines
		}
		return mcpText(string(core.TailLines([]byte(cleaned), lines))), nil, nil
	}))
}

func addSendTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_send",
		Description: "Inject input into a session without attaching — how you give a peer its task or answer its prompt.",
	}, appTool(app, func(_ context.Context, a *App, args mcpSendArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.ResolveOne(args.Session)
		if err != nil {
			return nil, nil, err
		}
		if err := a.Manager.Send(sess, args.Text, !args.NoEnter); err != nil {
			return nil, nil, err
		}
		return mcpText("sent"), nil, nil
	}))
}

func addOpenTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_open",
		Description: "Start a new agent or service session from a definition (af_defs) or ad-hoc flags. Returns the new session object.",
	}, appTool(app, func(_ context.Context, a *App, args mcpOpenArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.Open(core.OpenRequest{
			Definition: args.Definition,
			Name:       args.Name,
			Harness:    args.Harness,
			Cmd:        args.Cmd,
			Model:      args.Model,
			WorkDir:    args.WorkDir,
		})
		if err != nil {
			return nil, nil, err
		}
		return mcpJSON(sess.JSON())
	}))
}

// af_wait timeout bounds (the client passes seconds).
const (
	mcpWaitDefault = 60 * time.Second
	mcpWaitMax     = 600 * time.Second
)

func addWaitTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_wait",
		Description: "Block until a session reaches a target status — the coordination primitive. Default targets mean \"the peer stopped working\".",
	}, appTool(app, func(ctx context.Context, a *App, args mcpWaitArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.ResolveOne(args.Session)
		if err != nil {
			return nil, nil, err
		}
		forCSV := args.For
		if forCSV == "" {
			forCSV = "idle,awaiting-input,done"
		}
		targets := map[core.Status]bool{}
		for name := range strings.SplitSeq(forCSV, ",") {
			status, parseErr := core.ParseStatus(strings.TrimSpace(name))
			if parseErr != nil {
				return nil, nil, parseErr
			}
			targets[status] = true
		}
		timeout := mcpWaitDefault
		if args.TimeoutSeconds > 0 {
			timeout = min(time.Duration(args.TimeoutSeconds)*time.Second, mcpWaitMax)
		}
		sess, outcome, err := a.Manager.WaitContext(ctx, sess.ID, targets, timeout, time.Second)
		if err != nil {
			return nil, nil, err
		}
		return mcpJSON(map[string]any{
			"status":    sess.Status,
			"timed_out": outcome == core.WaitTimeout,
		})
	}))
}

func addCloseTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_close",
		Description: "Gracefully stop a session you opened (quit keys, then escalating signals).",
	}, appTool(app, func(_ context.Context, a *App, args mcpSessionArgs) (*mcp.CallToolResult, any, error) {
		sess, err := a.Manager.ResolveOne(args.Session)
		if err != nil {
			return nil, nil, err
		}
		if err := a.Manager.Close(sess, 0); err != nil {
			return nil, nil, err
		}
		return mcpText("closed"), nil, nil
	}))
}

func addDefsTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_defs",
		Description: "List reusable agent definitions available to af_open.",
	}, appTool(app, func(_ context.Context, a *App, _ struct{}) (*mcp.CallToolResult, any, error) {
		defs, err := a.Store.ListDefinitions()
		if err != nil {
			return nil, nil, err
		}
		out := []core.DefinitionJSON{}
		for _, d := range defs {
			out = append(out, d.JSON())
		}
		return mcpJSON(out)
	}))
}

func addSignalTool(server *mcp.Server, app func() (*App, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "af_signal",
		Description: "Report a harness state: call with state \"done\" when your task is complete so peers waiting on you unblock (session defaults to yourself).",
	}, appTool(app, func(_ context.Context, a *App, args mcpSignalArgs) (*mcp.CallToolResult, any, error) {
		ref := args.Session
		if ref == "" {
			ref = os.Getenv("AF_SESSION_ID")
		}
		if ref == "" {
			return nil, nil, core.Errf(core.ExitUsage, "no session given and AF_SESSION_ID is not set")
		}
		sess, err := a.Manager.ResolveOne(ref)
		if err != nil {
			return nil, nil, err
		}
		if err := a.Manager.Signal(sess, core.Status(args.State)); err != nil {
			return nil, nil, err
		}
		return mcpText("signaled " + args.State), nil, nil
	}))
}

func mcpText(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func mcpJSON(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return mcpText(string(b)), nil, nil
}
