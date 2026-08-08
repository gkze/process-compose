package config

import "testing"

func TestForkUpdateFeaturesDefaultDisabled(t *testing.T) {
	if CheckForUpdates != "false" {
		t.Errorf("CheckForUpdates = %q, want false for a source-built fork binary", CheckForUpdates)
	}
	if SelfUpdateEnabled != "false" {
		t.Errorf("SelfUpdateEnabled = %q, want false for a source-built fork binary", SelfUpdateEnabled)
	}
}
