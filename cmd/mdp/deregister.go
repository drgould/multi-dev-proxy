package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var deregisterCmd = &cobra.Command{
	Use:   "deregister <name>",
	Short: "Remove a server from all proxies",
	RunE:  runDeregister,
	Args:  cobra.ExactArgs(1),
}

func init() {
	rootCmd.AddCommand(deregisterCmd)
	deregisterCmd.Flags().Int("control-port", 13100, "Control API port")
}

func runDeregister(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	name := args[0]

	if !isOrchestratorRunning(controlPort) {
		return fmt.Errorf("no orchestrator running on port %d", controlPort)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("http://127.0.0.1:%d/__mdp/register/%s", controlPort, name),
		nil,
	)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("orchestrator not reachable: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK      bool `json:"ok"`
		Deleted bool `json:"deleted"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if !result.Deleted {
		fmt.Printf("No server named %q found\n", name)
		return nil
	}
	fmt.Printf("Deregistered %q\n", name)
	return nil
}
