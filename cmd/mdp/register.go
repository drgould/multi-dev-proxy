package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register <name>",
	Short: "Register a service with the proxy",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("register: not yet implemented")
	},
	Args: cobra.MaximumNArgs(1),
}

func init() {
	rootCmd.AddCommand(registerCmd)

	registerCmd.Flags().IntP("port", "p", 0, "Port the service is running on (required)")
	registerCmd.Flags().Int("pid", 0, "Process ID of the service")
	registerCmd.Flags().IntP("proxy-port", "P", 3000, "Proxy port to connect to")
	registerCmd.Flags().BoolP("list", "l", false, "List registered services")
}
