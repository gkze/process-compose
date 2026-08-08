package cmd

import (
	"errors"
	"testing"

	"github.com/f1bonacc1/process-compose/src/config"
)

func TestRunVersionUpdateCanBeDisabledAtBuildTime(t *testing.T) {
	original := config.SelfUpdateEnabled
	config.SelfUpdateEnabled = "false"
	t.Cleanup(func() { config.SelfUpdateEnabled = original })

	if err := runVersionUpdate(); !errors.Is(err, errSelfUpdateDisabled) {
		t.Fatalf("runVersionUpdate() error = %v, want %v", err, errSelfUpdateDisabled)
	}
}
