package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a command through the proxy",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("run: not yet implemented")
	},
	Args:               cobra.ArbitraryArgs,
	DisableFlagParsing: false,
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().IntP("proxy-port", "P", 3000, "Proxy port to connect to")
	runCmd.Flags().String("repo", "", "Repository name")
	runCmd.Flags().String("name", "", "Service name")
	runCmd.Flags().String("port-range", "10000-60000", "Range of ports for proxied services")
}
