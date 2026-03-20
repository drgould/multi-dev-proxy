package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register <name>",
	Short: "Register a service with the proxy",
	RunE:  runRegister,
	Args:  cobra.MaximumNArgs(1),
}

func init() {
	rootCmd.AddCommand(registerCmd)
	registerCmd.Flags().IntP("port", "p", 0, "Port the service is running on")
	registerCmd.Flags().Int("pid", 0, "Process ID of the service")
	registerCmd.Flags().IntP("proxy-port", "P", 3000, "Proxy port to connect to")
	registerCmd.Flags().BoolP("list", "l", false, "List registered services")
}

func runRegister(cmd *cobra.Command, args []string) error {
	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	listFlag, _ := cmd.Flags().GetBool("list")

	if envPort := os.Getenv("MDP_PROXY_PORT"); envPort != "" && !cmd.Flags().Changed("proxy-port") {
		fmt.Sscanf(envPort, "%d", &proxyPort)
	}

	proxyURL := discoverProxyURL(proxyPort)

	if listFlag {
		return listServers(proxyURL)
	}

	if len(args) == 0 {
		return fmt.Errorf("name is required (or use --list to list servers)")
	}
	name := args[0]

	port, _ := cmd.Flags().GetInt("port")
	if port <= 0 {
		return fmt.Errorf("--port is required and must be positive")
	}
	pid, _ := cmd.Flags().GetInt("pid")

	body, _ := json.Marshal(map[string]any{"name": name, "port": port, "pid": pid})
	req, _ := http.NewRequest(http.MethodPost, proxyURL+"/__mdp/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := tlsSkipClient().Do(req)
	if err != nil {
		return fmt.Errorf("proxy not reachable at %s: %w", proxyURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register failed (%d): %s", resp.StatusCode, b)
	}
	fmt.Printf("Registered %s on port %d\n", name, port)
	return nil
}

func tlsSkipClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func listServers(proxyURL string) error {
	resp, err := tlsSkipClient().Get(proxyURL + "/__mdp/servers")
	if err != nil {
		return fmt.Errorf("proxy not reachable at %s: %w", proxyURL, err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(result) == 0 {
		fmt.Println("No servers registered.")
		return nil
	}
	for repo, servers := range result {
		fmt.Printf("[%s]\n", repo)
		if m, ok := servers.(map[string]any); ok {
			for name := range m {
				fmt.Printf("  %s\n", name)
			}
		}
	}
	return nil
}

func discoverProxyURL(port int) string {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for _, scheme := range []string{"https", "http"} {
		u := fmt.Sprintf("%s://localhost:%d", scheme, port)
		resp, err := client.Get(u + "/__mdp/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return u
		}
	}
	return fmt.Sprintf("https://localhost:%d", port)
}
