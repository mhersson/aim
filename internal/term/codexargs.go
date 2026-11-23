package term

import "strings"

// CodexOptions reports whether args select a local TUI and whether they already set --no-daemon.
func CodexOptions(args []string) (bool, bool) {
	private := false
	positional := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		key, _, attached := strings.Cut(arg, "=")
		switch key {
		case "--no-daemon":
			private = true
		case "--":
			return true, private
		case "--remote", "--remote-auth-token-env", "--help", "-h", "--version", "-V":
			return false, private
		case "-c", "--config", "--enable", "--disable", "-i", "--image", "-m", "--model", "--local-provider", "-p", "--profile", "-s", "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval":
			if !attached {
				i++
			}

			continue
		}

		if strings.HasPrefix(arg, "-") || positional {
			continue
		}

		positional = true

		switch arg {
		case "exec", "e", "review", "login", "logout", "mcp", "mcp-server", "plugin", "app-server", "remote-control", "completion", "update", "doctor", "sandbox", "debug", "apply", "a", "queue", "archive", "delete", "migrate-rollouts", "migrate-nfs-db", "unarchive", "cloud", "exec-server", "tcp-tunnel", "git-credential", "execpolicy", "responses-api-proxy", "features", "help", "agents":
			return false, private
		}
	}

	return true, private
}
