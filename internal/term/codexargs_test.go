package term

import "testing"

func TestInteractiveCodex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args  []string
		local bool
	}{
		{nil, true},
		{[]string{"app"}, false},
		{[]string{"tcp-tunnel"}, false},
		{[]string{"git-credential"}, false},
		{[]string{"execpolicy"}, false},
		{[]string{"responses-api-proxy"}, false},
		{[]string{"--model", "exec", "resume", "id"}, true},
		{[]string{"--cd", "resume", "exec", "hello"}, false},
		{[]string{"resume", "--remote=unix://"}, false},
		{[]string{"--", "exec"}, true},
		{[]string{"hello world"}, true},
	} {
		if got, _ := CodexOptions(tc.args); got != tc.local {
			t.Errorf("InteractiveCodex(%q) = %v", tc.args, got)
		}
	}
}
