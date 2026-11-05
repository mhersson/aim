package harness

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// describeModel is the model of the one-off, whatever the user's default.
const describeModel = "sonnet"

// Describe forks the agent's session when req names one, so the run knows why
// the changes were made, and otherwise runs fresh on the commits and the diff
// summary. The run has no tools: --tools "" leaves the MCP servers' tools,
// which --strict-mcp-config drops.
func (c Claude) Describe(ctx context.Context, req DescribeRequest) (string, error) {
	args := []string{"--print", "--model", describeModel}
	if req.Session != "" {
		args = append(args, "--resume", string(req.Session), "--fork-session")
	}

	args = append(args, "--no-session-persistence", "--tools", "", "--strict-mcp-config")

	run := c.oneOff
	if run == nil {
		run = runClaude
	}

	out, err := run(ctx, req.Dir, args, describePrompt(req))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// describePrompt asks for the body alone, since the reply becomes the body as
// it is.
func describePrompt(req DescribeRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Write the description (the body) of a GitHub pull request for the branch %s, to be merged into %s.\n", req.Branch, req.Base)

	if req.Session != "" {
		b.WriteString("Use what this conversation tells about why these changes were made.\n")
	}

	b.WriteString("Reply with the body only, in GitHub-flavored Markdown: no title, no preamble or closing remarks, and no code fence around it.\n")

	block := func(intro, tag, text string) {
		fmt.Fprintf(&b, "\n%s\n<%s>\n%s", intro, tag, text)

		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "</%s>\n", tag)
	}

	if strings.TrimSpace(req.Template) != "" {
		block("Follow the sections of the repository's pull request template and fill each one in:", "template", req.Template)
	}

	if len(req.Commits) > 0 {
		var cs strings.Builder

		for _, c := range req.Commits {
			cs.WriteString("- " + c.Subject + "\n")

			if body := strings.TrimSpace(c.Body); body != "" {
				for line := range strings.SplitSeq(body, "\n") {
					cs.WriteString("  " + line + "\n")
				}
			}
		}

		block("The commits on the branch, oldest first:", "commits", cs.String())
	}

	if strings.TrimSpace(req.Stat) != "" {
		block("The files it changes (git diff --stat):", "stat", req.Stat)
	}

	return b.String()
}

// runClaude runs the first claude on PATH that is not AIM's own binary, which
// a nested AIM's PATH holds as the wrapper.
func runClaude(ctx context.Context, dir string, args []string, stdin string) (string, error) {
	var self os.FileInfo

	if exe, err := os.Executable(); err == nil {
		self, _ = os.Stat(exe)
	}

	return runOneOff(ctx, os.Environ(), self, dir, args, stdin)
}
