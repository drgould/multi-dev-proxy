package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the multi-dev proxy server",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("start: not yet implemented")
	},
}

func init() {
	rootCmd.AddCommand(startCmd)

	startCmd.Flags().IntP("port", "p", 3000, "Port to listen on")
	startCmd.Flags().String("host", "0.0.0.0", "Host to listen on")
	startCmd.Flags().String("tls-cert", "", "Path to TLS certificate file")
	startCmd.Flags().String("tls-key", "", "Path to TLS key file")
	startCmd.Flags().String("port-range", "10000-60000", "Range of ports for proxied services")
}
