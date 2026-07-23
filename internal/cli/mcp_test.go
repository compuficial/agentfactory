package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentfactory.sh/af/internal/core"
)

// mcpClient wires an in-memory client to the af MCP server, built the
// same way `af mcp` builds it (fresh App per tool call, config from the
// testEnv environment variables).
func mcpClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	root := NewRoot() // unset flags: config comes from AF_* env
	server := newMCPServer(func() (*App, error) { return newApp(root) })

	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "af-test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned a tool error: %+v", name, res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: unexpected content type %T", name, res.Content[0])
	}
	return text.Text
}

func TestMCPToolFlow(t *testing.T) {
	testEnv(t)
	cs := mcpClient(t)

	// The full tool surface is advertised.
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"af_status": true, "af_peek": true, "af_logs": true, "af_send": true,
		"af_open": true, "af_wait": true, "af_close": true, "af_defs": true, "af_signal": true,
	}
	for _, tool := range tools.Tools {
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}

	// Open an echoer via the tool, then coordinate with it.
	openOut := callTool(t, cs, "af_open", map[string]any{
		"cmd": "sh " + fixture(t, "echoer.sh"), "name": "mcped",
	})
	var opened core.SessionJSON
	if err := json.Unmarshal([]byte(openOut), &opened); err != nil {
		t.Fatalf("af_open output: %v\n%s", err, openOut)
	}
	waitFor(t, 5*time.Second, "echoer ready", logContains(t, opened.ID, "ready"))

	statusOut := callTool(t, cs, "af_status", nil)
	if !strings.Contains(statusOut, opened.ID) {
		t.Fatalf("af_status must list the opened session:\n%s", statusOut)
	}

	callTool(t, cs, "af_send", map[string]any{"session": "mcped", "text": "hello over mcp"})
	waitFor(t, 5*time.Second, "echo visible", logContains(t, opened.ID, "echo: hello over mcp"))
	if logsOut := callTool(t, cs, "af_logs", map[string]any{"session": opened.ID}); !strings.Contains(logsOut, "echo: hello over mcp") {
		t.Fatalf("af_logs must show the echo:\n%s", logsOut)
	}

	// Peer reports done; a waiter unblocks on it.
	callTool(t, cs, "af_signal", map[string]any{"session": opened.ID, "state": "done"})
	waitOut := callTool(t, cs, "af_wait", map[string]any{"session": opened.ID, "for": "done", "timeout_seconds": 10})
	var waited struct {
		Status   string `json:"status"`
		TimedOut bool   `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(waitOut), &waited); err != nil {
		t.Fatalf("af_wait output: %v\n%s", err, waitOut)
	}
	if waited.Status != "done" || waited.TimedOut {
		t.Fatalf("want done/false, got %+v", waited)
	}

	callTool(t, cs, "af_close", map[string]any{"session": opened.ID})
	final := statusJSON(t, opened.ID)
	if final.Status != "exited" && final.Status != "failed" {
		t.Fatalf("closed session should be terminal, got %s", final.Status)
	}
}
