package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, proxies, servers, and groups",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Int("control-port", 13100, "Control API port")
	statusCmd.Flags().Bool("json", false, "Output as JSON")
}

type statusData struct {
	Daemon   daemonStatus   `json:"daemon"`
	Proxies  []proxyStatus  `json:"proxies"`
	Groups   map[string][]string `json:"groups"`
	Services []serviceStatus `json:"services"`
}

type daemonStatus struct {
	Running     bool `json:"running"`
	PID         int  `json:"pid,omitempty"`
	ControlPort int  `json:"controlPort"`
}

type proxyStatus struct {
	Port    int            `json:"port"`
	Label   string         `json:"label,omitempty"`
	Default string         `json:"default,omitempty"`
	Servers []serverStatus `json:"servers"`
}

type serverStatus struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	PID   int    `json:"pid,omitempty"`
	Group string `json:"group,omitempty"`
}

type serviceStatus struct {
	Name   string `json:"name"`
	Group  string `json:"group,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Port   int    `json:"port,omitempty"`
	Status string `json:"status"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	data := statusData{
		Daemon: daemonStatus{ControlPort: controlPort},
		Groups: make(map[string][]string),
	}

	data.Daemon.PID = readPID()

	client := &http.Client{Timeout: 2 * time.Second}
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)

	resp, err := client.Get(controlURL + "/__mdp/health")
	if err != nil {
		data.Daemon.Running = false
		if jsonOutput {
			return outputJSON(data)
		}
		fmt.Println("mdp daemon: not running")
		if data.Daemon.PID > 0 {
			fmt.Printf("  stale PID file: %d\n", data.Daemon.PID)
		}
		return nil
	}
	resp.Body.Close()
	data.Daemon.Running = true

	if err := fetchProxies(client, controlURL, &data); err != nil {
		return err
	}
	if err := fetchGroups(client, controlURL, &data); err != nil {
		return err
	}
	if err := fetchServices(client, controlURL, &data); err != nil {
		return err
	}

	if jsonOutput {
		return outputJSON(data)
	}
	printStatus(data)
	return nil
}

func readPID() int {
	b, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

func fetchProxies(client *http.Client, base string, data *statusData) error {
	resp, err := client.Get(base + "/__mdp/proxies")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var raw []struct {
		Port       int    `json:"port"`
		Label      string `json:"label"`
		Default    string `json:"default"`
		Servers    []struct {
			Name  string `json:"name"`
			Port  int    `json:"port"`
			PID   int    `json:"pid"`
			Group string `json:"group"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil
	}
	for _, p := range raw {
		ps := proxyStatus{Port: p.Port, Label: p.Label, Default: p.Default}
		for _, s := range p.Servers {
			ps.Servers = append(ps.Servers, serverStatus{
				Name: s.Name, Port: s.Port, PID: s.PID, Group: s.Group,
			})
		}
		sort.Slice(ps.Servers, func(i, j int) bool {
			return ps.Servers[i].Name < ps.Servers[j].Name
		})
		data.Proxies = append(data.Proxies, ps)
	}
	sort.Slice(data.Proxies, func(i, j int) bool {
		return data.Proxies[i].Port < data.Proxies[j].Port
	})
	return nil
}

func fetchGroups(client *http.Client, base string, data *statusData) error {
	resp, err := client.Get(base + "/__mdp/groups")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&data.Groups)
	return nil
}

func fetchServices(client *http.Client, base string, data *statusData) error {
	resp, err := client.Get(base + "/__mdp/services")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&data.Services)
	return nil
}

func outputJSON(data statusData) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func printStatus(data statusData) {
	if data.Daemon.PID > 0 {
		fmt.Printf("mdp daemon: running (PID %d, ctrl :%d)\n", data.Daemon.PID, data.Daemon.ControlPort)
	} else {
		fmt.Printf("mdp daemon: running (ctrl :%d)\n", data.Daemon.ControlPort)
	}

	totalServers := 0
	for _, p := range data.Proxies {
		totalServers += len(p.Servers)
	}
	fmt.Printf("  %d proxy(s), %d server(s), %d group(s)\n\n",
		len(data.Proxies), totalServers, len(data.Groups))

	for _, p := range data.Proxies {
		label := p.Label
		if label == "" {
			label = "proxy"
		}
		fmt.Printf(":%d  %s\n", p.Port, label)
		if len(p.Servers) == 0 {
			fmt.Println("  (no servers)")
		}
		for _, s := range p.Servers {
			marker := "  "
			if s.Name == p.Default {
				marker = "● "
			}
			pidStr := ""
			if s.PID > 0 {
				pidStr = fmt.Sprintf("  PID %d", s.PID)
			}
			groupStr := ""
			if s.Group != "" {
				groupStr = fmt.Sprintf("  [%s]", s.Group)
			}
			fmt.Printf("  %s%-25s :%d%s%s\n", marker, s.Name, s.Port, pidStr, groupStr)
		}
		fmt.Println()
	}

	if len(data.Groups) > 0 {
		fmt.Println("Groups:")
		names := make([]string, 0, len(data.Groups))
		for name := range data.Groups {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			members := data.Groups[name]
			fmt.Printf("  %-20s %s\n", name, strings.Join(members, ", "))
		}
		fmt.Println()
	}

	managed := 0
	for _, svc := range data.Services {
		if svc.Status != "" {
			managed++
		}
	}
	if managed > 0 {
		fmt.Println("Services:")
		for _, svc := range data.Services {
			if svc.Status == "" {
				continue
			}
			fmt.Printf("  %-25s %s\n", svc.Name, svc.Status)
		}
		fmt.Println()
	}
}
