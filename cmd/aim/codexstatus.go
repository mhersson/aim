package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

const (
	codexStatusURI  = "ui://aim/statusline"
	codexStatusMeta = "com.openai/codex-statusline-state"
	// codexSendBudget bounds one delivery so a dead socket never backs up the sender.
	codexSendBudget = 500 * time.Millisecond
)

// newCodexStatuslineCmd is started by Codex as its status line provider, an
// MCP server on stdio. It writes only JSON-RPC to stdout and never fails.
func newCodexStatuslineCmd(getenv func(string) string) *cobra.Command {
	return &cobra.Command{
		Use:    "codex-statusline",
		Short:  "Serve Codex's status line provider and report to the running AIM",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Errors are dropped: main would print them to stderr.
			_ = serveCodexStatus(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), codexSender(cmd.Context(), getenv))

			return nil
		},
	}
}

// codexSender returns the send func for this process's pane. It sends nothing
// unless AIM's environment is complete and this Codex owns the pane's terminal.
func codexSender(ctx context.Context, getenv func(string) string) func(harness.Event) bool {
	return func(ev harness.Event) bool {
		sock, pane := getenv("AIM_SOCK"), getenv("AIM_PANE")
		if sock == "" || pane == "" {
			return false
		}

		owner, err := strconv.Atoi(getenv("AIM_CODEX_OWNER"))
		if err != nil || !term.ForegroundOwner(getenv("AIM_TTY"), owner) {
			return false
		}

		ev.Pane = core.PaneID(pane)
		ev.OwnerPID = owner

		sctx, cancel := context.WithTimeout(ctx, codexSendBudget)
		defer cancel()

		return harness.Send(sctx, sock, ev) == nil
	}
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	} `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// serveCodexStatus runs the JSON-RPC loop until in closes or ctx ends. Every
// read is answered with null at once, because an error or a read slower than
// a second stops Codex polling; delivery to AIM happens on a separate
// goroutine that sends only snapshots that changed.
func serveCodexStatus(ctx context.Context, in io.Reader, out io.Writer, send func(harness.Event) bool) error {
	mailbox := make(chan harness.Event, 1)
	sent := make(chan struct{})

	go func() {
		defer close(sent)

		var last harness.Event

		have := false

		for ev := range mailbox {
			if ctx.Err() != nil {
				continue
			}

			if have && sameStatus(last, ev) {
				continue
			}

			if send(ev) {
				last, have = ev, true
			}
		}
	}()

	defer func() {
		close(mailbox)
		<-sent
	}()

	// Reading in a goroutine lets ctx end the loop while stdin is idle.
	lines := make(chan []byte)

	go func() {
		defer close(lines)

		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), maxHookPayload)

		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)

			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	enc := json.NewEncoder(out)

	for {
		var line []byte

		select {
		case l, ok := <-lines:
			if !ok {
				return nil
			}

			line = l
		case <-ctx.Done():
			return nil
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			if enc.Encode(rpcReply{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}) != nil {
				return nil
			}

			continue
		}

		if len(req.ID) == 0 {
			continue
		}

		reply := rpcReply{JSONRPC: "2.0", ID: req.ID}

		switch req.Method {
		case "initialize":
			reply.Result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
				"serverInfo":      map[string]any{"name": "aim", "version": versionString(stamp{version, commit, date}, buildInfo())},
			}
		case "tools/list":
			reply.Result = map[string]any{"tools": []any{map[string]any{
				"name":        "statusline",
				"description": "Feeds AIM's display.",
				"inputSchema": map[string]any{"type": "object"},
				"_meta": map[string]any{"openai/ui": map[string]any{"entrypoints": []any{
					map[string]any{"type": "statusline", "resourceUri": codexStatusURI},
				}}},
			}}}
		case "tools/call":
			reply.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "This tool only feeds AIM's display."}}}
		case "resources/list":
			reply.Result = map[string]any{"resources": []any{}}
		case "resources/read":
			if ev, err := (harness.Codex{}).ParseStatus(req.Params.Meta[codexStatusMeta]); err == nil {
				offer(mailbox, ev)
			}

			reply.Result = map[string]any{"contents": []any{map[string]any{
				"uri": codexStatusURI, "mimeType": "application/json", "text": "null",
			}}}
		case "ping":
			reply.Result = map[string]any{}
		default:
			reply.Error = &rpcError{-32601, "method not found"}
		}

		if enc.Encode(reply) != nil {
			return nil
		}
	}
}

// offer puts ev in the one-slot mailbox, replacing an event not yet taken.
// Only the loop goroutine writes, so the drain-then-put never blocks.
func offer(mailbox chan harness.Event, ev harness.Event) {
	select {
	case <-mailbox:
	default:
	}

	mailbox <- ev
}

func sameStatus(a, b harness.Event) bool {
	return a.Session == b.Session && a.ThreadName == b.ThreadName && a.RunState == b.RunState &&
		a.Model == b.Model && a.Context == b.Context && a.ContextKnown == b.ContextKnown
}
