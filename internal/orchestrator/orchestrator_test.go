package orchestrator

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func newTestOrch() *Orchestrator {
	return New(&config.Config{}, false, "", "", "")
}

func TestNewSetsDefaults(t *testing.T) {
	o := New(nil, false, "", "", "")
	if o.host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %q", o.host)
	}
	if o.proxies == nil || o.services == nil || o.events == nil {
		t.Fatal("maps/channels should be initialized")
	}
}

func TestNewCustomHost(t *testing.T) {
	o := New(nil, true, "cert.pem", "key.pem", "127.0.0.1")
	if o.host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %q", o.host)
	}
	if !o.useTLS {
		t.Error("expected useTLS to be true")
	}
}

func TestRegisterAndSnapshot(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	pi := &ProxyInstance{
		Port:     3000,
		Label:    "frontend",
		Registry: registry.New(),
		cancel:   func() {},
	}
	o.proxies[3000] = pi
	o.mu.Unlock()

	entry := &registry.ServerEntry{Name: "app/main", Repo: "app", Port: 4001, PID: 100, Group: "dev"}
	if err := o.Register(3000, entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	snap := o.Snapshot()
	if len(snap.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(snap.Proxies))
	}
	if len(snap.Proxies[0].Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(snap.Proxies[0].Servers))
	}
	if snap.Proxies[0].Servers[0].Name != "app/main" {
		t.Errorf("expected server name app/main, got %q", snap.Proxies[0].Servers[0].Name)
	}
}

func TestSetDefaultAndClearDefault(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	reg := registry.New()
	reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 4001})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg, cancel: func() {}}
	o.mu.Unlock()

	if err := o.SetDefault(3000, "app/main"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	snap := o.Snapshot()
	if snap.Proxies[0].Default != "app/main" {
		t.Errorf("expected default app/main, got %q", snap.Proxies[0].Default)
	}

	if err := o.ClearDefault(3000); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	snap = o.Snapshot()
	if snap.Proxies[0].Default != "" {
		t.Errorf("expected empty default, got %q", snap.Proxies[0].Default)
	}
}

func TestSetDefaultMissingProxy(t *testing.T) {
	o := newTestOrch()
	if err := o.SetDefault(9999, "app/main"); err == nil {
		t.Error("expected error for missing proxy")
	}
}

func TestClearDefaultMissingProxy(t *testing.T) {
	o := newTestOrch()
	if err := o.ClearDefault(9999); err == nil {
		t.Error("expected error for missing proxy")
	}
}

func TestGroups(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	reg1 := registry.New()
	reg1.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 4001, Group: "dev"})
	reg1.Register(&registry.ServerEntry{Name: "app/staging", Repo: "app", Port: 4002, Group: "staging"})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg1, cancel: func() {}}

	reg2 := registry.New()
	reg2.Register(&registry.ServerEntry{Name: "api/dev", Repo: "api", Port: 5001, Group: "dev"})
	o.proxies[3001] = &ProxyInstance{Port: 3001, Registry: reg2, cancel: func() {}}
	o.mu.Unlock()

	groups := o.Groups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["dev"]) != 2 {
		t.Errorf("expected 2 members in dev, got %d", len(groups["dev"]))
	}
	if len(groups["staging"]) != 1 {
		t.Errorf("expected 1 member in staging, got %d", len(groups["staging"]))
	}
}

func TestSwitchGroup(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	reg1 := registry.New()
	reg1.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 4001, Group: "dev"})
	reg1.Register(&registry.ServerEntry{Name: "app/staging", Repo: "app", Port: 4002, Group: "staging"})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg1, cancel: func() {}}

	reg2 := registry.New()
	reg2.Register(&registry.ServerEntry{Name: "api/dev", Repo: "api", Port: 5001, Group: "dev"})
	o.proxies[3001] = &ProxyInstance{Port: 3001, Registry: reg2, cancel: func() {}}
	o.mu.Unlock()

	if err := o.SwitchGroup("dev"); err != nil {
		t.Fatalf("SwitchGroup: %v", err)
	}

	if d := reg1.GetDefault(); d != "app/dev" {
		t.Errorf("expected default app/dev on proxy 3000, got %q", d)
	}
	if d := reg2.GetDefault(); d != "api/dev" {
		t.Errorf("expected default api/dev on proxy 3001, got %q", d)
	}
}

func TestSwitchGroupNotFound(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: registry.New(), cancel: func() {}}
	o.mu.Unlock()

	if err := o.SwitchGroup("nonexistent"); err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestSetServiceAndListServices(t *testing.T) {
	o := newTestOrch()
	svc := &ManagedService{Name: "web/main", Group: "dev", PID: 42, Port: 4001, Status: "running"}
	o.SetService("web/main", svc)

	services := o.ListServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Status != "running" {
		t.Errorf("expected running, got %q", services[0].Status)
	}
}

func TestUpdateServiceStatus(t *testing.T) {
	o := newTestOrch()
	svc := &ManagedService{Name: "web/main", Group: "dev", PID: 42, Port: 4001, Status: "running"}
	o.SetService("web/main", svc)

	o.UpdateServiceStatus("web/main", "stopped")
	services := o.ListServices()
	if services[0].Status != "stopped" {
		t.Errorf("expected stopped, got %q", services[0].Status)
	}

	// updating non-existent service is a no-op
	o.UpdateServiceStatus("nonexistent", "failed")
}

func TestGetProxy(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: registry.New(), cancel: func() {}}
	o.mu.Unlock()

	if pi := o.GetProxy(3000); pi == nil {
		t.Error("expected to find proxy on port 3000")
	}
	if pi := o.GetProxy(9999); pi != nil {
		t.Error("expected nil for missing proxy")
	}
}

func TestListProxies(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: registry.New(), cancel: func() {}}
	o.proxies[3001] = &ProxyInstance{Port: 3001, Registry: registry.New(), cancel: func() {}}
	o.mu.Unlock()

	proxies := o.ListProxies()
	if len(proxies) != 2 {
		t.Errorf("expected 2 proxies, got %d", len(proxies))
	}
}

func TestSnapshotIncludesServices(t *testing.T) {
	o := newTestOrch()
	o.mu.Lock()
	o.proxies[3000] = &ProxyInstance{Port: 3000, Label: "web", Registry: registry.New(), cancel: func() {}}
	o.mu.Unlock()
	o.SetService("web/main", &ManagedService{Name: "web/main", Group: "dev", PID: 42, Port: 4001, Status: "running"})

	snap := o.Snapshot()
	if len(snap.Services) != 1 {
		t.Fatalf("expected 1 service in snapshot, got %d", len(snap.Services))
	}
	if snap.Services[0].Name != "web/main" {
		t.Errorf("expected service name web/main, got %q", snap.Services[0].Name)
	}
	if snap.Proxies[0].Label != "web" {
		t.Errorf("expected proxy label web, got %q", snap.Proxies[0].Label)
	}
}

func TestShutdown(t *testing.T) {
	o := newTestOrch()
	cancelled := false

	// Create a real http.Server so Shutdown doesn't nil-panic
	srv := &http.Server{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(listener)

	o.mu.Lock()
	o.proxies[3000] = &ProxyInstance{
		Port:     3000,
		Registry: registry.New(),
		Server:   srv,
		cancel:   func() { cancelled = true },
	}
	o.mu.Unlock()

	o.Shutdown(context.Background())

	if !cancelled {
		t.Error("expected cancel to be called on shutdown")
	}
	if len(o.proxies) != 0 {
		t.Errorf("expected proxies to be cleared after shutdown, got %d", len(o.proxies))
	}
}

func TestEvents(t *testing.T) {
	o := newTestOrch()
	ch := o.Events()
	o.emit(Event{Type: "test_event", Name: "foo"})

	select {
	case e := <-ch:
		if e.Type != "test_event" {
			t.Errorf("expected test_event, got %q", e.Type)
		}
	default:
		t.Error("expected event on channel")
	}
}
