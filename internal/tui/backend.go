package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// ConnState describes the health of the backend's connection to the daemon.
type ConnState int

const (
	ConnConnected ConnState = iota
	ConnReconnecting
	ConnLost
)

// LogSource describes one log file the daemon can serve.
type LogSource struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	SizeBytes int64  `json:"sizeBytes"`
}

// LogChunk is one cursor read of a log file.
type LogChunk struct {
	Lines      []string `json:"lines"`
	NextOffset int64    `json:"nextOffset"`
	Truncated  bool     `json:"truncated"`
}

// Backend is the interface the TUI uses to interact with the orchestrator,
// either locally (in-process) or remotely (via the control API).
type Backend interface {
	Events() <-chan orchestrator.Event
	Snapshot() orchestrator.Snapshot
	SwitchGroup(name string) error
	SetDefault(proxyPort int, name string) error
	StopServer(name string) error
	ConnState() ConnState
	ListLogs() ([]LogSource, error)
	FetchLog(id string, offset int64) (LogChunk, error)
}

// RemoteBackend connects to a running orchestrator via the control API.
type RemoteBackend struct {
	controlURL string
	client     *http.Client
	sseClient  *http.Client // no timeout: holds the long-lived event stream
	events     chan orchestrator.Event
	stopPoll   chan struct{}
	stopOnce   sync.Once
	connState  atomic.Int32
}

// NewRemoteBackend creates a backend that polls the control API for liveness
// and subscribes to its SSE stream for low-latency change notifications.
func NewRemoteBackend(controlPort int) *RemoteBackend {
	rb := &RemoteBackend{
		controlURL: fmt.Sprintf("http://127.0.0.1:%d", controlPort),
		client:     &http.Client{Timeout: 2 * time.Second},
		sseClient:  &http.Client{},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}
	go rb.poll()
	go rb.sse()
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
				rb.setConnState(ConnConnected)
				select {
				case rb.events <- orchestrator.Event{Type: "poll"}:
				default:
				}
			} else {
				failCount++
				if failCount >= 3 {
					rb.setConnState(ConnLost)
					select {
					case rb.events <- orchestrator.Event{Type: "daemon_lost"}:
					default:
					}
					return
				}
				rb.setConnState(ConnReconnecting)
			}
		}
	}
}

// ConnState reports the current connection state.
func (rb *RemoteBackend) ConnState() ConnState {
	return ConnState(rb.connState.Load())
}

func (rb *RemoteBackend) setConnState(s ConnState) {
	rb.connState.Store(int32(s))
}

// sse subscribes to the daemon's /__mdp/events stream and forwards each ping
// as an "update" event; the health poll remains the liveness authority, so
// stream errors just retry until Stop.
func (rb *RemoteBackend) sse() {
	for {
		rb.streamEvents()
		select {
		case <-rb.stopPoll:
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func (rb *RemoteBackend) streamEvents() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-rb.stopPoll:
			cancel()
		case <-done:
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rb.controlURL+"/__mdp/events", nil)
	if err != nil {
		return
	}
	resp, err := rb.sseClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data:") {
			continue
		}
		select {
		case rb.events <- orchestrator.Event{Type: "update"}:
		default:
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

// StopServer asks the daemon to gracefully stop the process behind a
// registered server; the daemon signals it asynchronously.
func (rb *RemoteBackend) StopServer(name string) error {
	payload, _ := json.Marshal(map[string]string{"name": name})
	resp, err := rb.client.Post(rb.controlURL+"/__mdp/servers/stop", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
		return fmt.Errorf("stop %s: %s", name, body.Error)
	}
	return fmt.Errorf("stop %s failed (status %d)", name, resp.StatusCode)
}

// ListLogs fetches the log sources the daemon can serve.
func (rb *RemoteBackend) ListLogs() ([]LogSource, error) {
	resp, err := rb.client.Get(rb.controlURL + "/__mdp/logs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list logs failed (status %d)", resp.StatusCode)
	}
	var sources []LogSource
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// FetchLog reads one cursor chunk of a log file; a negative offset means
// "the last |offset| bytes".
func (rb *RemoteBackend) FetchLog(id string, offset int64) (LogChunk, error) {
	resp, err := rb.client.Get(fmt.Sprintf("%s/__mdp/logs/%s?offset=%d", rb.controlURL, url.PathEscape(id), offset))
	if err != nil {
		return LogChunk{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LogChunk{}, fmt.Errorf("fetch log failed (status %d)", resp.StatusCode)
	}
	var chunk LogChunk
	if err := json.NewDecoder(resp.Body).Decode(&chunk); err != nil {
		return LogChunk{}, err
	}
	return chunk, nil
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
		servers := make([]registry.ServerEntry, 0, len(rp.Servers))
		for _, rs := range rp.Servers {
			servers = append(servers, registry.ServerEntry{
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
