package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var switchCmd = &cobra.Command{
	Use:   "switch [name]",
	Short: "Switch active upstream service or group",
	RunE:  runSwitch,
	Args:  cobra.MaximumNArgs(1),
}

func init() {
	rootCmd.AddCommand(switchCmd)
	switchCmd.Flags().IntP("proxy-port", "P", 0, "Proxy port (for individual server switch)")
	switchCmd.Flags().String("group", "", "Switch all services in a group")
	switchCmd.Flags().Bool("clear", false, "Clear the default upstream")
	switchCmd.Flags().Int("control-port", 13100, "Control API port")
}

func runSwitch(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	groupName, _ := cmd.Flags().GetString("group")
	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	clear, _ := cmd.Flags().GetBool("clear")

	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	client := &http.Client{Timeout: 5 * time.Second}

	if groupName != "" {
		resp, err := client.Post(controlURL+"/__mdp/groups/"+groupName+"/switch", "application/json", nil)
		if err != nil {
			return fmt.Errorf("orchestrator not reachable: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("switch group failed (status %d)", resp.StatusCode)
		}
		fmt.Printf("Switched to group %q\n", groupName)
		return nil
	}

	if clear {
		if proxyPort <= 0 {
			return fmt.Errorf("--proxy-port is required with --clear")
		}
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/__mdp/proxies/%d/default", controlURL, proxyPort), nil)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("orchestrator not reachable: %w", err)
		}
		resp.Body.Close()
		fmt.Printf("Cleared default on proxy :%d\n", proxyPort)
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("name is required (or use --group)")
	}
	name := args[0]

	if proxyPort <= 0 {
		return fmt.Errorf("--proxy-port is required for individual server switch")
	}

	resp, err := client.Post(fmt.Sprintf("%s/__mdp/proxies/%d/default/%s", controlURL, proxyPort, name), "application/json", nil)
	if err != nil {
		return fmt.Errorf("orchestrator not reachable: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("switch failed (status %d)", resp.StatusCode)
	}
	fmt.Printf("Switched proxy :%d to %q\n", proxyPort, name)
	return nil
}
