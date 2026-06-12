package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open the web dashboard in your browser",
	RunE:  runDashboard,
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
	dashboardCmd.Flags().Int("control-port", 13100, "Control API port")
	dashboardCmd.Flags().Int("dashboard-port", 0, "Dashboard web UI port (overrides the port reported by the daemon)")
}

func runDashboard(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")

	dashboardPort, ok := fetchDashboardPort(controlPort)
	if !ok {
		return fmt.Errorf("mdp orchestrator is not running — start it with 'mdp' or 'mdp -d'")
	}
	if cmd.Flags().Changed("dashboard-port") {
		dashboardPort, _ = cmd.Flags().GetInt("dashboard-port")
	}
	if dashboardPort <= 0 {
		return fmt.Errorf("dashboard is not running — check %s", logFilePath())
	}

	url := fmt.Sprintf("http://localhost:%d", dashboardPort)
	fmt.Println(url)
	return openBrowser(url)
}

// fetchDashboardPort queries the running daemon's control API for the actual
// dashboard port. ok is false if the control API is unreachable; port is 0 if
// the daemon's dashboard server is not running.
func fetchDashboardPort(controlPort int) (port int, ok bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/__mdp/health", controlPort))
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var body struct {
		DashboardPort int `json:"dashboardPort"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false
	}
	return body.DashboardPort, true
}
