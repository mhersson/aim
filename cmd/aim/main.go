// Command aim is the AIM binary: CLI and wiring.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/store"
	"github.com/mhersson/aim/internal/ui"
)

// closeTimeout bounds how long quitting waits for pane processes to end.
const closeTimeout = 3 * time.Second

func main() {
	if filepath.Base(os.Args[0]) == shimName {
		os.Exit(runShim(os.Args[1:], os.Stderr))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	err := newRootCmd().ExecuteContext(ctx)

	stop()

	if err != nil {
		fmt.Fprintf(os.Stderr, "aim: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	env, envErr := core.DefaultEnv()

	var (
		configPath, recordDir string
		fresh                 bool
	)

	cmd := &cobra.Command{
		Use:           "aim",
		Version:       versionString(stamp{version, commit, date}, buildInfo()),
		Short:         "Coding agents in terminal panes, with their diffs and pull requests beside them",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if envErr != nil {
				return envErr
			}
			// Only the default path may be missing; a path the user named must exist.
			if cmd.Flags().Changed("config") {
				if _, err := os.Stat(configPath); err != nil {
					return fmt.Errorf("config: %w", err)
				}
			}

			return run(cmd.Context(), env, configPath, recordDir, fresh, githubClients)
		},
	}

	cmd.SetVersionTemplate("aim {{.Version}}\n")

	defaultConfig := ""
	if envErr == nil {
		defaultConfig = core.ConfigPath(env)
	}

	cmd.Flags().StringVar(&configPath, "config", defaultConfig, "config file")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "start with one shell instead of the saved layout; the saved layout is replaced as AIM starts")
	cmd.Flags().StringVar(&recordDir, "record", "", "write each pane's terminal output to a recording in this directory")
	// Diagnostics for bench inputs and VT fixtures, not a user feature.
	_ = cmd.Flags().MarkHidden("record")
	cmd.AddCommand(newHookCmd(os.Getenv), newStatuslineCmd(os.Getenv))

	return cmd
}

// run loads the config and opens the log before the TUI takes over the
// terminal, so their errors can still be printed.
func run(ctx context.Context, env core.Env, configPath, recordDir string, fresh bool, clients clientFactory, progOpts ...tea.ProgramOption) (err error) {
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return err
	}

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("config %s: %w", configPath, err)
	}

	stateDir := core.StateDir(env)

	// The lock comes first, so a second instance never moves the holder's log.
	held, unlock, err := lockState(stateDir)
	if err != nil {
		return err
	}

	f, err := openLog(stateDir, held, os.Getpid())
	if err != nil {
		_ = unlock()

		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close log: %w", cerr)
		}
	}()

	logger := slog.New(slog.NewTextHandler(f, nil))
	// Held until the end of the run: a dropped unlock lets the finalizer release the lock.
	defer func() {
		if uerr := unlock(); uerr != nil {
			logger.Warn("release state lock", "err", uerr)
		}
	}()

	if held {
		sweepLogs(logger, stateDir, time.Now(), pidAlive)
	}

	indexCache := indexCacheDir(logger)

	if n, err := git.SweepIndexCache(indexCache, time.Now()); err != nil {
		logger.Warn("sweep index cache", "err", err)
	} else if n > 0 {
		logger.Info("swept index cache", "removed", n)
	}

	watches := git.NewWatchSet(context.Background(), git.WatchOptions{})
	defer func() {
		if cerr := watches.Close(); cerr != nil {
			logger.Warn("close watch set", "err", cerr)
		}
	}()

	places := git.NewPlaceCache(git.EditPlace, time.Now)

	hooks := startHooks(logger, stateDir, indexCache, watches, places, cfg.Shell.IntegrationOn())
	defer hooks.Stop()

	st, saver, notices := openState(logger, stateDir, cfg, fresh, held)

	// A second instance that does not save keeps no mark files either.
	var marks *store.MarkFiles
	if saver != nil {
		marks = store.NewMarkFiles(stateDir, logger)
	}

	// The saver is closed before any pane is, so agents dying on quit are not saved as shells.
	closeSaver := sync.OnceFunc(func() {
		if saver == nil {
			return
		}

		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()

		if cerr := saver.Close(closeCtx); cerr != nil {
			logger.Error("close saver", "err", cerr)
		}

		if cerr := marks.Close(closeCtx); cerr != nil {
			logger.Error("close mark files", "err", cerr)
		}
	})
	defer closeSaver()

	var save func(core.State)
	if saver != nil {
		save = saver.Save
	}

	if hooks.Notice != "" {
		notices = append(notices, hooks.Notice)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("launch directory: %w", err)
	}

	prs := startPRs(clients)

	app, err := ui.New(ui.Options{
		Logger: logger,
		Store:  core.NewStore(st),
		Getenv: os.Getenv,
		Cwd:    cwd,
		Icons:  cfg.IconsOn(),
		Diff:   cfg.Diff,
		Keys:   cfg.Keys,

		Worktrees:    cfg.Worktrees,
		Plans:        cfg.Plans,
		PullRequests: cfg.PullRequests,

		FocusFollowsMouse: cfg.Mouse.FocusFollowsMouse,
		CopyOnSelect:      cfg.Mouse.CopyOnSelect,
		NoOSC52:           !cfg.Clipboard.OSC52On(),
		NoNativeClipboard: !cfg.Clipboard.NativeOn(),

		RecordDir:  recordDir,
		ConfigPath: configPath,

		Shell:        hooks.Shell,
		HookSettings: hooks.Settings,
		HookSock:     hooks.Sock,
		Events:       hooks.Events,
		GitTempDir:   hooks.RunDir,
		IndexCache:   indexCache,
		SweepRefs:    saver != nil,
		Pictures:     github.NewImageFetcher(github.ImageOptions{}),
		ImageCache:   imageCacheDir(indexCache),
		Git:          ui.SystemGit{},
		Watches:      watches,
		Places:       places,
		PRs:          prs.poller,
		PRLookup:     prs.lookup,
		PRActions:    prs.actions(),
		PRReads:      prs.reads(),
		Notice:       strings.Join(notices, "; "),
		Save:         save,
		Marks:        markFilesOf(marks),
	})
	if err != nil {
		_ = prs.Close()

		return err
	}

	logger.Info("start", "config", configPath, "theme", cfg.Theme)
	// ctx is the only signal handler, so SIGINT, SIGTERM and SIGHUP (a closed
	// terminal) end the run cleanly instead of racing Bubble Tea's own handler.
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler()}, progOpts...)
	_, err = tea.NewProgram(app, opts...).Run()

	closeSaver()
	// The context may be cancelled already, so the close gets its own deadline.
	closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	closeRun(closeCtx, logger, app, prs)

	if err != nil && ctx.Err() == nil {
		logger.Error("run", "err", err)

		return fmt.Errorf("run: %w", err)
	}

	logger.Info("exit")

	return nil
}

// lockState creates stateDir if needed and takes the state lock in it. held is
// false when another instance holds the lock, and unlock is then a no-op. The
// caller keeps unlock reachable until the run ends.
func lockState(stateDir string) (held bool, unlock func() error, err error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return false, nil, fmt.Errorf("state dir: %w", err)
	}

	unlock, err = store.Lock(stateDir)
	if errors.Is(err, store.ErrLocked) {
		return false, func() error { return nil }, nil
	}

	if err != nil {
		return false, nil, fmt.Errorf("state lock: %w", err)
	}

	return true, unlock, nil
}

// openState loads the saved state when this instance holds the state lock.
// Otherwise it starts fresh and gets no saver.
func openState(logger *slog.Logger, stateDir string, cfg core.Config, fresh, held bool) (st core.State, saver *store.Saver, notices []string) {
	if !held {
		return core.InitialState(cfg), nil, []string{noticeSecondInstance}
	}

	statePath := filepath.Join(stateDir, "state.json")

	st, notices = loadState(statePath, fresh, cfg)

	var resume []string

	st, resume = checkResume(st, transcriptExists)
	notices = append(notices, resume...)

	return st, store.NewSaver(statePath, logger), notices
}

// markFilesOf returns m as the UI's interface, or nil (not a typed nil) when
// this instance does not save.
func markFilesOf(m *store.MarkFiles) interface {
	Load(commonDir string, now time.Time) (map[string]string, error)
	Save(commonDir string, days map[string]string)
} {
	if m == nil {
		return nil
	}

	return m
}
