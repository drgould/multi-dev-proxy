package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// Backend is the interface the TUI uses to interact with the orchestrator,
// either locally (in-process) or remotely (via the control API).
type Backend interface {
	Events() <-chan orchestrator.Event
	Snapshot() orchestrator.Snapshot
	SwitchGroup(name string) error
	SetDefault(proxyPort int, name string) error
}

// RemoteBackend connects to a running orchestrator via the control API.
type RemoteBackend struct {
	controlURL string
	client     *http.Client
	events     chan orchestrator.Event
	stopPoll   chan struct{}
	stopOnce   sync.Once
}

// NewRemoteBackend creates a backend that polls the control API.
func NewRemoteBackend(controlPort int) *RemoteBackend {
	rb := &RemoteBackend{
		controlURL: fmt.Sprintf("http://127.0.0.1:%d", controlPort),
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}
	go rb.poll()
	return rb
}

func (rb *RemoteBackend) poll() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	failCount := 0
	for {
		select {
		case <-rb.stopPoll:
			return
		case <-ticker.C:
			if rb.healthCheck() {
				failCount = 0
				select {
				case rb.events <- orchestrator.Event{Type: "poll"}:
				default:
				}
			} else {
				failCount++
				if failCount >= 3 {
					select {
					case rb.events <- orchestrator.Event{Type: "daemon_lost"}:
					default:
					}
					return
				}
			}
		}
	}
}

func (rb *RemoteBackend) healthCheck() bool {
	resp, err := rb.client.Get(rb.controlURL + "/__mdp/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Stop terminates the background poller.
func (rb *RemoteBackend) Stop() {
	rb.stopOnce.Do(func() { close(rb.stopPoll) })
}

func (rb *RemoteBackend) Events() <-chan orchestrator.Event {
	return rb.events
}

func (rb *RemoteBackend) Snapshot() orchestrator.Snapshot {
	snap := orchestrator.Snapshot{
		Groups: make(map[string][]string),
	}

	if proxies, err := rb.fetchProxies(); err == nil {
		snap.Proxies = proxies
	}
	if groups, err := rb.fetchGroups(); err == nil {
		snap.Groups = groups
	}
	if services, err := rb.fetchServices(); err == nil {
		snap.Services = services
	}

	return snap
}

func (rb *RemoteBackend) SwitchGroup(name string) error {
	resp, err := rb.client.Post(
		rb.controlURL+"/__mdp/groups/"+url.PathEscape(name)+"/switch",
		"application/json", nil,
	)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("switch group failed (status %d)", resp.StatusCode)
	}
	return nil
}

func (rb *RemoteBackend) SetDefault(proxyPort int, name string) error {
	resp, err := rb.client.Post(
		fmt.Sprintf("%s/__mdp/proxies/%d/default/%s", rb.controlURL, proxyPort, url.PathEscape(name)),
		"application/json", nil,
	)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set default failed (status %d)", resp.StatusCode)
	}
	return nil
}

type remoteProxy struct {
	Port       int              `json:"port"`
	Label      string           `json:"label"`
	CookieName string           `json:"cookieName"`
	Default    string           `json:"default"`
	Servers    []remoteServer   `json:"servers"`
}

type remoteServer struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	PID   int    `json:"pid"`
	Group string `json:"group"`
}

func (rb *RemoteBackend) fetchProxies() ([]orchestrator.ProxySnapshot, error) {
	resp, err := rb.client.Get(rb.controlURL + "/__mdp/proxies")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []remoteProxy
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	result := make([]orchestrator.ProxySnapshot, 0, len(raw))
	for _, rp := range raw {
		servers := make([]*registry.ServerEntry, 0, len(rp.Servers))
		for _, rs := range rp.Servers {
			servers = append(servers, &registry.ServerEntry{
				Name:  rs.Name,
				Port:  rs.Port,
				PID:   rs.PID,
				Group: rs.Group,
			})
		}
		result = append(result, orchestrator.ProxySnapshot{
			Port:       rp.Port,
			Label:      rp.Label,
			CookieName: rp.CookieName,
			Default:    rp.Default,
			Servers:    servers,
		})
	}
	return result, nil
}

func (rb *RemoteBackend) fetchGroups() (map[string][]string, error) {
	resp, err := rb.client.Get(rb.controlURL + "/__mdp/groups")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var groups map[string][]string
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		return nil, err
	}
	return groups, nil
}

type remoteService struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	PID    int    `json:"pid"`
	Port   int    `json:"port"`
	Status string `json:"status"`
}

func (rb *RemoteBackend) fetchServices() ([]orchestrator.ServiceSnapshot, error) {
	resp, err := rb.client.Get(rb.controlURL + "/__mdp/services")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []remoteService
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	result := make([]orchestrator.ServiceSnapshot, 0, len(raw))
	for _, rs := range raw {
		result = append(result, orchestrator.ServiceSnapshot{
			Name:   rs.Name,
			Group:  rs.Group,
			PID:    rs.PID,
			Port:   rs.Port,
			Status: rs.Status,
		})
	}
	return result, nil
}
