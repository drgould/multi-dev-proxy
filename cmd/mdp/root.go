package main

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "mdp",
	Short:   "Multi-dev proxy — run multiple dev servers behind one port",
	Version: version + " (" + commit + ")",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// subcommands registered in their own files via init()
}
