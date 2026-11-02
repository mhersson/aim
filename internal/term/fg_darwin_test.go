package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Claude's native install runs claude as a symlink to a file named after its
// version. A symlink to sleep stands in for it: p_comm reports "sleep".
func TestForegroundSymlinkedAgent(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			sh, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s not installed", name)
			}

			shellTestEnv(t)
			t.Setenv("SHELL", sh)

			link := filepath.Join(t.TempDir(), "claude")
			if err := os.Symlink("/bin/sleep", link); err != nil {
				t.Fatal(err)
			}

			p := startCommand(t, AgentCommand(os.Getenv, Integration{}, t.TempDir(), []string{link, "30"}, nil))

			waitForeground(t, p, "claude")

			pgrp, err := foregroundPgrp(p.pty)
			if err != nil {
				t.Fatal(err)
			}

			if comm, err := kinfoComm(pgrp); err != nil || comm != "sleep" {
				t.Fatalf("p_comm = %q, %v; want sleep, else this no longer tests the symlink case", comm, err)
			}
		})
	}
}
