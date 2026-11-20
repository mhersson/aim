package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	nativeTimeout = 2 * time.Second
	// nativeWaitDelay bounds how long Wait lingers on I/O after the context
	// ends; wl-copy and xclip fork a child that keeps serving the selection.
	nativeWaitDelay = 100 * time.Millisecond
)

type clipboardConfig struct{ OSC52, Native bool }

// clipboard writes copied text to the system clipboard through OSC 52 and,
// on a local session, through native tools, and an image through those tools
// alone. It also reads the primary selection (the clipboard on macOS) back for
// a middle-click paste.
type clipboard struct {
	cfg     clipboardConfig
	getenv  func(string) string
	goos    string
	log     *slog.Logger
	timeout time.Duration
	run     func(ctx context.Context, argv []string, stdin string, stdout io.Writer) error

	// last is the latest non-empty text copy or copyPrimary was given; only the
	// update loop touches it.
	last string

	mu      sync.Mutex
	missing map[string]bool
}

func newClipboard(cfg clipboardConfig, getenv func(string) string, goos string, log *slog.Logger) *clipboard {
	return &clipboard{
		cfg:     cfg,
		getenv:  getenv,
		goos:    goos,
		log:     log,
		timeout: nativeTimeout,
		run:     runNative,
		missing: map[string]bool{},
	}
}

// copy returns a Cmd that sends text to the clipboard and the primary
// selection, or nil when there is nothing to send.
func (c *clipboard) copy(text string) tea.Cmd { return c.send(text, false) }

// copyPrimary is copy for the primary selection alone. macOS has none, so
// there it only records text as the last copy.
func (c *clipboard) copyPrimary(text string) tea.Cmd { return c.send(text, true) }

func (c *clipboard) send(text string, primaryOnly bool) tea.Cmd {
	if text == "" {
		return nil
	}

	c.last = text

	var cmds []tea.Cmd

	if c.cfg.OSC52 {
		if !primaryOnly {
			cmds = append(cmds, tea.SetClipboard(text))
		}

		if c.goos == "linux" {
			cmds = append(cmds, tea.SetPrimaryClipboard(text))
		}
	}

	if c.cfg.Native {
		argvs := nativeCommands(c.getenv, c.goos)
		if primaryOnly {
			argvs = slices.DeleteFunc(argvs, func(argv []string) bool { return !writesPrimary(argv) })
		}

		if len(argvs) > 0 {
			cmds = append(cmds, func() tea.Msg {
				c.writeNative(argvs, text)

				return nil
			})
		}
	}

	return tea.Batch(cmds...)
}

// writesPrimary reports whether a nativeCommands argv writes the primary selection.
func writesPrimary(argv []string) bool {
	return argv[len(argv)-1] == "--primary" || argv[len(argv)-1] == "primary"
}

func (c *clipboard) writeNative(argvs [][]string, text string) {
	for _, argv := range argvs {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		err := c.run(ctx, argv, text, nil)

		cancel()

		if err != nil {
			c.logFailure("write", argv, err)
		}
	}
}

// paste returns a Cmd that delivers the text to paste: the primary selection
// (the clipboard on macOS) when a native tool reads it; the last copy when none
// can be used or it is missing; nothing when a read fails.
func (c *clipboard) paste(deliver func(text string) tea.Msg) tea.Cmd {
	last := c.last

	var argv []string
	if c.cfg.Native {
		argv = pasteCommand(c.getenv, c.goos)
	}

	if argv == nil {
		return func() tea.Msg { return deliver(last) }
	}

	return func() tea.Msg {
		text, err := c.readNative(argv)
		if err == nil {
			return deliver(text)
		}

		c.logFailure("read", argv, err)

		if errors.Is(err, exec.ErrNotFound) {
			return deliver(last)
		}

		return deliver("")
	}
}

func (c *clipboard) readNative(argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	var buf bytes.Buffer
	if err := c.run(ctx, argv, "", &buf); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (c *clipboard) logFailure(op string, argv []string, err error) {
	if errors.Is(err, exec.ErrNotFound) {
		c.mu.Lock()
		seen := c.missing[argv[0]]
		c.missing[argv[0]] = true
		c.mu.Unlock()

		if seen {
			return
		}
	}

	c.log.Warn("native clipboard "+op+" failed", "cmd", strings.Join(argv, " "), "err", err)
}

// localSession is the desktop whose native clipboard tools a session can use.
type localSession int

const (
	sessionNone localSession = iota
	sessionDarwin
	sessionWayland
	sessionX11
)

// Why a session has no native clipboard tools.
const (
	reasonSSH       = "it runs over SSH"
	reasonNoDisplay = "neither WAYLAND_DISPLAY nor DISPLAY is set"
	reasonNativeOff = "[clipboard] native is off"
)

// errImageSession means the session has no native tool to copy an image with.
var errImageSession = errors.New("no native clipboard tool for an image")

// nativeSession says whose tools a session can use: none on an SSH session or
// when no display is known, and why.
func nativeSession(getenv func(string) string, goos string) (localSession, string) {
	switch {
	case getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "":
		return sessionNone, reasonSSH
	case goos == "darwin":
		return sessionDarwin, ""
	case getenv("WAYLAND_DISPLAY") != "":
		return sessionWayland, ""
	case getenv("DISPLAY") != "":
		return sessionX11, ""
	}

	return sessionNone, reasonNoDisplay
}

// nativeCommands returns the tools to write with, in order; none on an SSH
// session or when no display is known.
func nativeCommands(getenv func(string) string, goos string) [][]string {
	switch s, _ := nativeSession(getenv, goos); s {
	case sessionDarwin:
		return [][]string{{"pbcopy"}}
	case sessionWayland:
		return [][]string{{"wl-copy"}, {"wl-copy", "--primary"}}
	case sessionX11:
		return [][]string{
			{"xclip", "-selection", "clipboard"},
			{"xclip", "-selection", "primary"},
		}
	}

	return nil
}

// pasteCommand returns the tool to read the primary selection (the clipboard
// on macOS) with; nil on an SSH session or when no display is known.
func pasteCommand(getenv func(string) string, goos string) []string {
	switch s, _ := nativeSession(getenv, goos); s {
	case sessionDarwin:
		return []string{"pbpaste"}
	case sessionWayland:
		return []string{"wl-paste", "--primary", "--no-newline", "--type", "text"}
	case sessionX11:
		return []string{"xclip", "-selection", "primary", "-o"}
	}

	return nil
}

// imageSession is the session whose tools copy an image, and why there is
// none. OSC 52 carries text alone, so only native tools copy an image.
func (c *clipboard) imageSession() (localSession, string) {
	if !c.cfg.Native {
		return sessionNone, reasonNativeOff
	}

	return nativeSession(c.getenv, c.goos)
}

// copyImage puts png on the clipboard, not the primary selection: on the stdin
// of wl-copy or xclip, or on macOS, whose pbcopy takes text alone, through a
// file in dir that osascript reads.
func (c *clipboard) copyImage(ctx context.Context, png []byte, dir string) error {
	var (
		argv  []string
		stdin string
	)

	switch s, reason := c.imageSession(); s {
	case sessionWayland:
		argv, stdin = []string{"wl-copy", "--type", "image/png"}, string(png)
	case sessionX11:
		argv, stdin = []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-i"}, string(png)
	case sessionDarwin:
		file, err := writeClipPNG(dir, png)
		if err != nil {
			return err
		}

		defer func() { _ = os.Remove(file) }()

		argv = []string{"osascript", "-e", pngfScript(file)}
	default:
		return fmt.Errorf("%w: %s", errImageSession, reason)
	}

	return c.run(ctx, argv, stdin, nil)
}

// writeClipPNG writes png to a new file in dir for osascript to read.
func writeClipPNG(dir string, png []byte) (string, error) {
	if dir == "" {
		return "", errSentNoDir
	}

	f, err := os.CreateTemp(dir, ".clip-*.png")
	if err != nil {
		return "", fmt.Errorf("create png: %w", err)
	}

	_, werr := f.Write(png)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("write png: %w", err)
	}

	return f.Name(), nil
}

// pngfScript is the AppleScript that puts the PNG file at path on the
// clipboard; the path is quoted as an AppleScript string.
func pngfScript(path string) string {
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path)

	return `set the clipboard to (read (POSIX file "` + q + `") as «class PNGf»)`
}

// runNative runs argv. Writes pass a nil stdout so a forked selection server
// cannot hold a pipe open past the context; reads pass a buffer, and their
// tools do not fork.
func runNative(ctx context.Context, argv []string, stdin string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = stdout

	cmd.WaitDelay = nativeWaitDelay
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", argv[0], err)
	}

	return nil
}
