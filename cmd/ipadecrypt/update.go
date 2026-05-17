package main

import (
	"context"
	"time"

	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
)

func updateHandler(cmd *cobra.Command, args []string) {
	cfg, _, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	if updateCheckOnly && updateRollback {
		tui.Err("--check and --rollback are mutually exclusive")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if updateRollback {
		res, err := updater.RollbackCurrentExecutable()
		if err != nil {
			tui.Err("rollback failed: %v", err)
			return
		}
		tui.OK("rolled back CLI: %s", res.ExecutablePath)
		return
	}

	if updateCheckOnly {
		res, err := updater.CheckLatest(ctx, Version, cfg)
		if err != nil {
			tui.Err("update check failed: %v", err)
			return
		}
		if res.Updated {
			tui.Warn("update available: %s (you have %s)", res.LatestVersion, res.CurrentVersion)
			tui.Info("%s", res.ReleaseURL)
			return
		}
		if updater.IsDev(Version) {
			tui.Warn("latest release: %s (development build cannot compare)", res.LatestVersion)
			tui.Info("%s", res.ReleaseURL)
			return
		}
		tui.OK("CLI is up to date: %s", Version)
		return
	}

	tui.Info("checking latest release")
	res, err := updater.SelfUpdate(ctx, Version, cfg)
	if err != nil {
		tui.Err("update failed: %v", err)
		return
	}
	if !res.Updated {
		tui.OK("CLI is up to date: %s", Version)
		return
	}

	tui.OK("updated CLI to %s", res.LatestVersion)
	tui.Info("backup: %s", res.BackupPath)
}

func newUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"u"},
		Short:   "Update the ipadecrypt CLI",
		Args:    cobra.NoArgs,
		Run:     updateHandler,
	}
	cmd.Flags().BoolVar(&updateCheckOnly, "check", false, "check for an update without installing it")
	cmd.Flags().BoolVar(&updateRollback, "rollback", false, "restore the previous CLI binary backup")
	return cmd
}
