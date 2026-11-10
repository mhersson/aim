package main

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const (
	// hookBudget bounds the whole hook command so an agent is never held up.
	hookBudget = 2 * time.Second
	// maxHookPayload bounds how much of a hook or statusline payload is read from stdin.
	maxHookPayload = 16 << 20
)

// showNote is what a session-start hook tells the agent about aim show.
const showNote = "You are running inside AIM, a terminal multiplexer. To show the user an image, such as a screenshot or a before and after pair, save it as PNG, JPEG, GIF or WebP and run `\"$AIM_BIN\" show <file>... --caption \"<text>\"` (at most 8 files); AIM displays it to them straight away."

// newHookCmd is run by the agent for every hook event. It always succeeds
// quietly: a failing or noisy hook would disturb the agent it reports on.
func newHookCmd(getenv func(string) string) *cobra.Command {
	var note bool

	cmd := &cobra.Command{
		Use:    "hook <harness>",
		Short:  "Report an agent hook event to the running AIM",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), hookBudget)
			defer cancel()

			runHook(ctx, args[0], note, cmd.InOrStdin(), cmd.OutOrStdout(), getenv)

			return nil
		},
	}

	cmd.Flags().BoolVar(&note, "note", false, "print the aim show note at session start")

	return cmd
}

// runHook forwards one hook payload to the AIM socket. Its only output is the
// session-start note, which Claude Code adds to the agent's context; it drops
// the event on any failure.
func runHook(ctx context.Context, id string, note bool, stdin io.Reader, stdout io.Writer, getenv func(string) string) {
	payload, ok := readPayload(ctx, stdin)
	if !ok {
		return
	}

	h, ok := harness.ByID(id)
	if !ok {
		return
	}

	ev, err := h.ParseEvent(payload)
	if err != nil {
		return
	}

	if note && ev.Name == "SessionStart" && getenv("AIM_BIN") != "" {
		_, _ = io.WriteString(stdout, showNote+"\n")
	}

	sock, pane := getenv("AIM_SOCK"), getenv("AIM_PANE")
	if sock == "" || pane == "" {
		return
	}

	ev.Pane = core.PaneID(pane)
	_ = harness.Send(ctx, sock, ev)
}

// readPayload reads an agent's payload from stdin, at most maxHookPayload
// bytes, and reports false when the read fails or ctx ends first.
func readPayload(ctx context.Context, stdin io.Reader) ([]byte, bool) {
	// Read in a goroutine so a stdin that never closes cannot outlast the
	// budget; the process exits right after, which ends the reader.
	type result struct {
		data []byte
		err  error
	}

	read := make(chan result, 1)

	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, maxHookPayload))
		// Drain the rest so the agent's write never fails with EPIPE.
		_, _ = io.Copy(io.Discard, stdin)

		read <- result{data, err}
	}()

	select {
	case r := <-read:
		return r.data, r.err == nil
	case <-ctx.Done():
		return nil, false
	}
}
