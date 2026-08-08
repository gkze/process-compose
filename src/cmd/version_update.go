package cmd

import (
	"errors"

	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/f1bonacc1/process-compose/src/updater"
	"github.com/spf13/cobra"
)

var errSelfUpdateDisabled = errors.New("self-update is disabled in this build")

var versionUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update process-compose to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVersionUpdate()
	},
}

func runVersionUpdate() error {
	if config.SelfUpdateEnabled != "true" {
		return errSelfUpdateDisabled
	}
	return updater.Update()
}

func init() {
	versionCmd.AddCommand(versionUpdateCmd)
}
