package main

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// statuslineBudget bounds the whole statusline command: Claude runs it often
// and shows nothing from it, so a slow AIM must never hold it up.
const statuslineBudget = time.Second

// newStatuslineCmd is run by Claude Code as its status line. It always
// succeeds and prints nothing, so Claude's own status line stays empty.
func newStatuslineCmd(getenv func(string) string) *cobra.Command {
	return &cobra.Command{
		Use:    "statusline",
		Short:  "Report an agent's status line update to the running AIM",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), statuslineBudget)
			defer cancel()

			runStatusline(ctx, cmd.InOrStdin(), getenv)

			return nil
		},
	}
}

// runStatusline forwards one statusline payload to the AIM socket. It
// writes nothing to stdout or stderr and drops the update on any failure.
func runStatusline(ctx context.Context, stdin io.Reader, getenv func(string) string) {
	payload, ok := readPayload(ctx, stdin)
	if !ok {
		return
	}

	sock, pane := getenv("AIM_SOCK"), getenv("AIM_PANE")
	if sock == "" || pane == "" {
		return
	}

	ev, err := harness.Claude{}.ParseStatus(payload)
	if err != nil {
		return
	}

	ev.Harness = (harness.Claude{}).ID()
	ev.Pane = core.PaneID(pane)
	_ = harness.Send(ctx, sock, ev)
}
