package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/ui/kitty"
)

var (
	errNoAIM         = errors.New("not in an AIM pane: AIM_SOCK or AIM_PANE is not set")
	errTooMany       = fmt.Errorf("at most %d images at a time", harness.MaxImages)
	errNotFile       = errors.New("not a regular file")
	errNotImageName  = errors.New("not a .png, .jpg, .jpeg, .gif or .webp file")
	errTooBig        = errors.New("over 5 MiB")
	errSocketBlocked = errors.New("AIM's socket cannot be reached from here (a sandbox may block it); name the image's path in your final reply instead")
)

// newShowCmd shows images to the operator in the running AIM. Unlike a hook it
// reports failures, so the agent that ran it can tell.
func newShowCmd(getenv func(string) string) *cobra.Command {
	var caption string

	cmd := &cobra.Command{
		Use:   "show <file>...",
		Short: "Show images to the operator in the running AIM",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			line, err := runShow(cmd.Context(), args, caption, getenv, os.Getwd)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)

			return err
		},
	}

	cmd.Flags().StringVar(&caption, "caption", "", "a short text shown with the images")

	return cmd
}

// runShow checks every file, then sends them as one batch and returns the line
// to print. Nothing is sent unless every file passes.
func runShow(ctx context.Context, files []string, caption string, getenv func(string) string, getwd func() (string, error)) (string, error) {
	sock, pane := getenv("AIM_SOCK"), getenv("AIM_PANE")
	if sock == "" || pane == "" {
		return "", errNoAIM
	}

	if len(files) > harness.MaxImages {
		return "", errTooMany
	}

	paths := make([]string, 0, len(files))

	for _, f := range files {
		p := f

		if !filepath.IsAbs(p) {
			wd, err := getwd()
			if err != nil {
				return "", fmt.Errorf("%s: working directory: %w", f, err)
			}

			p = filepath.Join(wd, p)
		}

		p = filepath.Clean(p)

		if err := checkImage(p); err != nil {
			return "", fmt.Errorf("%s: %w", f, err)
		}

		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, hookBudget)
	defer cancel()

	ev := harness.Event{Pane: core.PaneID(pane), Name: "show", Show: true, Images: paths, Caption: harness.Caption(caption)}
	if err := harness.Send(ctx, sock, ev); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return "", errSocketBlocked
		}

		return "", fmt.Errorf("AIM did not answer: %w", err)
	}

	if len(paths) == 1 {
		return "sent 1 image to AIM", nil
	}

	return "sent " + strconv.Itoa(len(paths)) + " images to AIM", nil
}

// checkImage reports why AIM could not show the file at p: it must be a
// regular file named like an image, within the size cap and decodable.
func checkImage(p string) error {
	if len(p) > harness.MaxImagePath {
		return fmt.Errorf("path over %d bytes", harness.MaxImagePath)
	}

	if !harness.IsImagePath(p) {
		return errNotImageName
	}

	fi, err := os.Stat(p)
	if err != nil {
		return err
	}

	if !fi.Mode().IsRegular() {
		return errNotFile
	}

	// O_NONBLOCK keeps a file replaced by a FIFO since the stat from hanging the open.
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, kitty.MaxFileBytes+1))
	if err != nil {
		return err
	}

	if len(data) > kitty.MaxFileBytes {
		return errTooBig
	}

	_, _, err = kitty.Size(data)

	return err
}
