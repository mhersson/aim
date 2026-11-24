package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (Codex) ID() string                                         { return "codex" }
func (Codex) Program() string                                    { return "codex" }
func (Codex) CustomName(context.Context, string) (string, error) { return "", nil }

func (c Codex) Describe(ctx context.Context, req DescribeRequest) (string, error) {
	if req.RunDir == "" {
		return "", errors.New("codex: the run directory is unavailable")
	}

	dir, err := os.MkdirTemp(req.RunDir, fmt.Sprintf("%d-describe-", os.Getpid()))
	if err != nil {
		return "", fmt.Errorf("codex output directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	bodyPath := filepath.Join(dir, "body.md")
	if err := os.WriteFile(bodyPath, nil, 0o600); err != nil {
		return "", fmt.Errorf("create codex output: %w", err)
	}
	// Codex merges empty MCP tables. The overrides cover the listed servers,
	// assuming the server configuration stays stable until exec reads it.
	inventory, err := runProgram(ctx, c.Program(), req.Dir, []string{"--no-daemon", "mcp", "list", "--json"}, "")
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		if errors.Is(err, exec.ErrNotFound) {
			return "", err
		}

		return "", errors.New("codex: cannot list MCP servers")
	}

	var servers []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(inventory), &servers) != nil || servers == nil {
		return "", errors.New("codex: invalid MCP server list")
	}

	var disabled []string

	for _, server := range servers {
		if server.Name == "" {
			return "", errors.New("codex: unnamed MCP server")
		}

		name, _ := json.Marshal(server.Name)
		disabled = append(disabled, string(name)+"={enabled=false}")
	}

	args := []string{
		"--no-daemon", "exec", "--ephemeral", "--sandbox", "read-only",
		"-c", "features.shell_tool=false", "-c", "features.apps=false", "-c", "features.plugins=false",
		"-c", "features.computer_use=false", "-c", "features.browser_use=false",
		"-c", `web_search="disabled"`, "-c", "agents.enabled=false", "-c", "features.multi_agent_v2=false", "-c", `approval_policy="never"`,
		"-c", "mcp_servers={" + strings.Join(disabled, ",") + "}", "--color", "never", "--output-last-message", bodyPath, "-",
	}

	req.Session = ""
	if _, err := runProgram(ctx, c.Program(), req.Dir, args, describePrompt(req)); err != nil {
		return "", err
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return "", fmt.Errorf("read codex output: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}
