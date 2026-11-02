package term

import (
	"encoding/binary"
	"testing"
)

// procargs builds a kern.procargs2 buffer: argc, the exec path, pad NULs, each
// argument NUL-terminated, then an environment string, as the kernel lays it out.
func procargs(argc uint32, path string, pad int, argv ...string) []byte {
	b := binary.NativeEndian.AppendUint32(nil, argc)
	b = append(append(b, path...), 0)
	b = append(b, make([]byte, pad)...)

	for _, a := range append(argv, "SHELL=/bin/zsh") {
		b = append(append(b, a...), 0)
	}

	return b
}

func TestProcargsName(t *testing.T) {
	t.Parallel()

	// raw is argc and then s with no terminator added.
	raw := func(argc uint32, s string) []byte { return append(binary.NativeEndian.AppendUint32(nil, argc), s...) }

	tests := []struct {
		name string
		buf  []byte
		want string
		ok   bool
	}{
		{"bare name", procargs(2, "/Users/u/.local/bin/claude", 3, "claude", "--resume"), "claude", true},
		{"full path", procargs(1, "/tmp/x/claude", 0, "/tmp/x/claude"), "claude", true},
		{"login shell", procargs(1, "/bin/zsh", 2, "-zsh"), "zsh", true},
		{"npm claude", procargs(3, "/opt/homebrew/bin/node", 2, "node", "/opt/homebrew/bin/claude", "--resume"), "claude", true},
		{"bun claude", procargs(2, "/Users/u/.bun/bin/bun", 1, "bun", "/Users/u/.bun/bin/claude"), "claude", true},
		{"node script", procargs(2, "/opt/homebrew/bin/node", 0, "node", "/srv/app/server.js"), "server.js", true},
		{"node alone", procargs(1, "/opt/homebrew/bin/node", 0, "node"), "node", true},
		{"node option", procargs(3, "/opt/homebrew/bin/node", 0, "node", "--require=/x/claude", "app.js"), "node", true},
		{"node empty script", procargs(2, "/opt/homebrew/bin/node", 0, "node", ""), "node", true},
		{"script of another program", procargs(2, "/bin/sh", 0, "sh", "/x/claude"), "sh", true},
		{"node script unterminated", raw(2, "/opt/homebrew/bin/node\x00node\x00/x/claude"), "node", true},
		{"no argv", procargs(0, "/bin/sleep", 4), "", false},
		{"short buffer", []byte{1, 0}, "", false},
		{"exec path unterminated", raw(1, "/bin/sleep"), "", false},
		{"argv0 unterminated", raw(1, "/bin/sleep\x00\x00\x00sleep"), "", false},
		{"bare dash", procargs(1, "/bin/sh", 0, "-"), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := procargsName(tt.buf)
			if got != tt.want || ok != tt.ok {
				t.Errorf("procargsName = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
