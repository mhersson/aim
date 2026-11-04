package main

import (
	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui"
	"github.com/mhersson/aim/internal/ui/theme"
)

// validateConfig checks a loaded config's values against the themes AIM
// offers and returns the first error.
func validateConfig(cfg core.Config, themes theme.Set) error {
	if _, err := themes.ByName(cfg.Theme); err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}

	for _, check := range []func() error{
		cfg.Layout.Validate, cfg.Diff.Validate, cfg.Worktrees.Validate,
		cfg.Plans.Validate, cfg.PullRequests.Validate, cfg.Keys.Validate,
		func() error { return ui.CheckKeys(cfg.Keys, themes) },
	} {
		if err := check(); err != nil {
			return err //nolint:wrapcheck // the caller names the file
		}
	}

	return nil
}
