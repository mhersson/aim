package term

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"
)

// procargsName names a process from its kern.procargs2 buffer (argc as a
// uint32, the exec path, NUL padding, then argv): the base name of argv[0],
// without a login shell's leading "-", which Linux's comm does not have either.
// An empty argv[0] cannot be told from the padding, as in ps.
//
// For node or bun it is argv[1]'s base name, unless that is an option: an npm
// install's "#!/usr/bin/env node" makes claude's argv ["node", "<path>/claude"].
func procargsName(b []byte) (string, bool) {
	if len(b) < 4 {
		return "", false
	}

	argc := binary.NativeEndian.Uint32(b)
	if argc == 0 {
		return "", false
	}

	b = b[4:]

	end := bytes.IndexByte(b, 0)
	if end < 0 {
		return "", false
	}

	argv := bytes.Split(bytes.TrimLeft(b[end:], "\x00"), []byte{0})
	if len(argv) <= int(argc) {
		// A partial buffer may still identify argv[0].
		argv = argv[:len(argv)-1]
	} else {
		argv = argv[:argc]
	}

	return foregroundName(argv)
}

func foregroundName(argv [][]byte) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}

	name, ok := baseName(strings.TrimPrefix(string(argv[0]), "-"))
	if !ok {
		return "", false
	}

	if (name == "node" || name == "bun") && len(argv) > 1 && !bytes.HasPrefix(argv[1], []byte("-")) {
		script := string(argv[1])
		if n, valid := baseName(script); valid {
			name = n
			if strings.HasSuffix(script, "/node_modules/@openai/codex/bin/codex.js") {
				name = "codex"
			}

			argv = argv[1:]
		}
	}

	if name == "codex" {
		args := make([]string, 0, len(argv)-1)
		for _, arg := range argv[1:] {
			args = append(args, string(arg))
		}

		if local, _ := CodexOptions(args); !local {
			return "codex-cli", true
		}
	}

	return name, true
}

// baseName is the base name of path, false for an empty path or a root.
func baseName(path string) (string, bool) {
	name := filepath.Base(path)

	return name, name != "." && name != "/"
}

func (p *Process) Foreground() (string, error) {
	name, _, err := p.ForegroundProcess()

	return name, err
}
