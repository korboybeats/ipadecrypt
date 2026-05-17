package main

import (
	"fmt"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/spf13/cobra"
)

func keepHandler(cmd *cobra.Command, args []string) {
	cfg, _, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	current := cfg.OutputKeep()
	if keepShow {
		tui.OK("decrypted IPA keep policy: %s", current)
		return
	}

	next := ""
	if len(args) > 0 {
		next = args[0]
	} else {
		if !tui.IsTTY() {
			tui.Err("keep requires a value when not running in a terminal")
			tui.Info("use `ipadecrypt keep desktop`, `ipadecrypt keep device`, or `ipadecrypt keep both`")
			return
		}

		options, values := keepOptions(current)
		idx, err := tui.Select(fmt.Sprintf("where should decrypted IPAs be kept? (current: %s)", current), options)
		if err != nil {
			tui.Err("%v", err)
			return
		}
		next = values[idx]
	}

	keep, err := config.NormalizeOutputKeep(next)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	cfg.Output.Keep = keep
	if err := cfg.Save(); err != nil {
		tui.Err("save config: %v", err)
		return
	}

	tui.OK("decrypted IPA keep policy: %s", keep)
}

func keepOptions(current string) ([]string, []string) {
	all := []struct {
		label string
		value string
	}{
		{"Both", config.OutputKeepBoth},
		{"Desktop only", config.OutputKeepDesktop},
		{"Device only", config.OutputKeepDevice},
	}

	options := make([]string, 0, len(all))
	values := make([]string, 0, len(all))

	for _, opt := range all {
		if opt.value == current {
			options = append(options, opt.label)
			values = append(values, opt.value)
			break
		}
	}
	for _, opt := range all {
		if opt.value == current {
			continue
		}
		options = append(options, opt.label)
		values = append(values, opt.value)
	}

	return options, values
}
