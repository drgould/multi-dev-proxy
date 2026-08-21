package main

import "github.com/spf13/cobra"

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background daemon",
	RunE:  runStopCmd,
}

func init() {
	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().Int("control-port", 13100, "Control API port")
}

func runStopCmd(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	return runStop(controlPort)
}
