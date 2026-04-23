package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	registerCmd.Flags().String("group", "", "Group name override (default: git branch)")
	registerCmd.Flags().Int("control-port", 13100, "Orchestrator control port")
	registerCmd.Flags().String("tls-cert", "", "TLS certificate file (forwarded to proxy for HTTPS)")
	registerCmd.Flags().String("tls-key", "", "TLS key file (forwarded to proxy for HTTPS)")
}

func runRegister(cmd *cobra.Command, args []string) error {
	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	listFlag, _ := cmd.Flags().GetBool("list")
	controlPort, _ := cmd.Flags().GetInt("control-port")
	groupFlag, _ := cmd.Flags().GetString("group")
	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")

	if envPort := os.Getenv("MDP_PROXY_PORT"); envPort != "" && !cmd.Flags().Changed("proxy-port") {
		fmt.Sscanf(envPort, "%d", &proxyPort)
	}

	if (tlsCert != "") != (tlsKey != "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required")
	}

	// Resolve TLS paths against the caller's cwd — the daemon, which actually
	// loads the cert, may be running from a different directory.
	if tlsCert != "" {
		abs, err := filepath.Abs(tlsCert)
		if err != nil {
			return fmt.Errorf("resolve --tls-cert: %w", err)
		}
		tlsCert = abs
	}
	if tlsKey != "" {
		abs, err := filepath.Abs(tlsKey)
		if err != nil {
			return fmt.Errorf("resolve --tls-key: %w", err)
		}
		tlsKey = abs
	}

	if isOrchestratorRunning(controlPort) {
		return runRegisterViaOrchestrator(cmd, args, controlPort, proxyPort, groupFlag, listFlag, tlsCert, tlsKey)
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

	payload := map[string]any{"name": name, "port": port, "pid": pid, "group": groupFlag}
	if tlsCert != "" {
		payload["scheme"] = "https"
		payload["tlsCertPath"] = tlsCert
		payload["tlsKeyPath"] = tlsKey
	}
	body, _ := json.Marshal(payload)
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

func runRegisterViaOrchestrator(cmd *cobra.Command, args []string, controlPort, proxyPort int, groupFlag string, listFlag bool, tlsCert, tlsKey string) error {
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	client := &http.Client{Timeout: 5 * time.Second}

	if listFlag {
		resp, err := client.Get(controlURL + "/__mdp/proxies")
		if err != nil {
			return fmt.Errorf("orchestrator not reachable: %w", err)
		}
		defer resp.Body.Close()
		var result []map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		for _, p := range result {
			fmt.Printf("[:%v]\n", p["port"])
			if servers, ok := p["servers"].([]any); ok {
				for _, s := range servers {
					if srv, ok := s.(map[string]any); ok {
						fmt.Printf("  %s (:%v)\n", srv["name"], srv["port"])
					}
				}
			}
		}
		return nil
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

	payload := map[string]any{
		"name":      name,
		"port":      port,
		"pid":       pid,
		"proxyPort": proxyPort,
		"group":     groupFlag,
	}
	if tlsCert != "" {
		payload["scheme"] = "https"
		payload["tlsCertPath"] = tlsCert
		payload["tlsKeyPath"] = tlsKey
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("orchestrator not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register failed (%d): %s", resp.StatusCode, b)
	}
	fmt.Printf("Registered %s on port %d (proxy :%d)\n", name, port, proxyPort)
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
