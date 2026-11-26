package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (Codex) ID() string                                         { return "codex" }
func (Codex) Program() string                                    { return "codex" }
func (Codex) CustomName(context.Context, string) (string, error) { return "", nil }

func (c Codex) Describe(ctx context.Context, req DescribeRequest) (string, error) {
	// Codex merges empty MCP tables. The overrides cover the listed servers,
	// assuming the server configuration stays stable until exec reads it.
	inventory, err := runProgram(ctx, c.Program(), req.Dir, []string{"--no-daemon", "mcp", "list", "--json"}, "")
	if err != nil {
		return "", fmt.Errorf("codex: list MCP servers: %w", err)
	}

	var servers []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(inventory), &servers) != nil || servers == nil {
		return "", errors.New("codex: invalid MCP server list")
	}

	var disabled []string

	for _, server := range servers {
		name, _ := json.Marshal(server.Name)
		disabled = append(disabled, string(name)+"={enabled=false}")
	}

	args := []string{
		"--no-daemon", "exec", "--ephemeral", "--sandbox", "read-only",
		"-c", "features.shell_tool=false", "-c", "features.apps=false", "-c", "features.plugins=false",
		"-c", "features.computer_use=false", "-c", "features.browser_use=false",
		"-c", `web_search="disabled"`, "-c", "agents.enabled=false", "-c", "features.multi_agent_v2=false", "-c", `approval_policy="never"`,
		"-c", "mcp_servers={" + strings.Join(disabled, ",") + "}", "--color", "never", "-",
	}

	out, err := runProgram(ctx, c.Program(), req.Dir, args, describePrompt(req))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}
