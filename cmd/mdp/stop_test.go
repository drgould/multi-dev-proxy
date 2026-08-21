package main

import "testing"

func TestStopCmdRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("mdp stop not registered: %v", err)
	}
	if cmd != stopCmd {
		t.Fatalf("expected stopCmd, got %v", cmd)
	}
}

func TestRootStopFlagDeprecated(t *testing.T) {
	f := rootCmd.Flags().Lookup("stop")
	if f == nil {
		t.Fatal("--stop flag should still exist")
	}
	if f.Deprecated == "" {
		t.Error("--stop flag should be marked deprecated")
	}
}
