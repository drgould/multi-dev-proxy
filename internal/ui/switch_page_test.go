package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

func TestRenderSwitchPageEmpty(t *testing.T) {
	html := RenderSwitchPage(nil, "")
	if !strings.Contains(html, "No dev servers registered") {
		t.Fatal("empty page should contain 'No dev servers registered'")
	}
	if !strings.Contains(html, "mdp run") {
		t.Fatal("empty page should mention 'mdp run'")
	}
}

func TestRenderSwitchPageSingleServer(t *testing.T) {
	servers := []*registry.ServerEntry{
		{Name: "myrepo/main", Repo: "myrepo", Port: 3000, PID: 1234},
	}
	html := RenderSwitchPage(servers, "myrepo/main")
	if !strings.Contains(html, "main") {
		t.Fatal("should contain branch name 'main'")
	}
	if !strings.Contains(html, "Active") {
		t.Fatal("active server should show 'Active' badge")
	}
	if !strings.Contains(html, ":3000") {
		t.Fatal("should contain port")
	}
	if !strings.Contains(html, "1234") {
		t.Fatal("should contain PID")
	}
}

func TestRenderSwitchPageGrouped(t *testing.T) {
	servers := []*registry.ServerEntry{
		{Name: "alpha/feat-1", Repo: "alpha", Port: 3001, PID: 100},
		{Name: "beta/feat-2", Repo: "beta", Port: 3002, PID: 200},
	}
	html := RenderSwitchPage(servers, "")
	if !strings.Contains(html, "alpha") {
		t.Fatal("should contain repo name 'alpha'")
	}
	if !strings.Contains(html, "beta") {
		t.Fatal("should contain repo name 'beta'")
	}

	alphaIdx := strings.Index(html, `class="repo-name">alpha`)
	betaIdx := strings.Index(html, `class="repo-name">beta`)
	if alphaIdx < 0 || betaIdx < 0 {
		t.Fatal("both repos should appear as repo-name headings")
	}
	if alphaIdx > betaIdx {
		t.Fatal("repos should be sorted alphabetically (alpha before beta)")
	}
}

func TestRenderSwitchPageActiveHighlighted(t *testing.T) {
	servers := []*registry.ServerEntry{
		{Name: "repo/branch-a", Repo: "repo", Port: 3001, PID: 100},
		{Name: "repo/branch-b", Repo: "repo", Port: 3002, PID: 200},
	}
	html := RenderSwitchPage(servers, "repo/branch-a")

	if !strings.Contains(html, `class="active"`) {
		t.Fatal("active row should have class='active'")
	}
	if !strings.Contains(html, `badge-active">Active`) {
		t.Fatal("active server should show Active badge")
	}
	if !strings.Contains(html, `btn">Switch`) {
		t.Fatal("inactive server should show Switch button")
	}
}

func TestRenderSwitchPageValidHTML(t *testing.T) {
	html := RenderSwitchPage(nil, "")
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Fatal("should start with <!DOCTYPE html>")
	}
	if !strings.HasSuffix(strings.TrimSpace(html), "</html>") {
		t.Fatal("should end with </html>")
	}
}

func TestAutoRefreshPresent(t *testing.T) {
	html := RenderSwitchPage(nil, "")
	if !strings.Contains(html, "setTimeout") {
		t.Fatal("should contain setTimeout for auto-refresh")
	}
	if !strings.Contains(html, "location.reload()") {
		t.Fatal("should contain location.reload()")
	}
}

func TestSwitchPageHandler(t *testing.T) {
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{
		Name: "app/main",
		Repo: "app",
		Port: 3000,
		PID:  42,
	})

	handler := SwitchPageHandler(reg)
	req := httptest.NewRequest(http.MethodGet, "/__mdp/switch", nil)
	req.Header.Set("Cookie", routing.CookieName+"=app%2Fmain")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "main") {
		t.Fatal("response should contain server branch name")
	}
	if !strings.Contains(body, "Active") {
		t.Fatal("response should show Active badge for cookie-matched server")
	}
}
