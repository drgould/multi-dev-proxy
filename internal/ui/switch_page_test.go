package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderSwitchPageValidHTML(t *testing.T) {
	page := renderSwitchPage()
	if !strings.HasPrefix(page, "<!DOCTYPE html>") {
		t.Fatal("should start with <!DOCTYPE html>")
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Fatal("should end with </html>")
	}
}

func TestRenderSwitchPageContainsFetchEndpoints(t *testing.T) {
	page := renderSwitchPage()
	if !strings.Contains(page, "/__mdp/servers") {
		t.Fatal("should fetch /__mdp/servers")
	}
	if !strings.Contains(page, "/__mdp/config") {
		t.Fatal("should fetch /__mdp/config")
	}
}

func TestRenderSwitchPageUsesSSE(t *testing.T) {
	page := renderSwitchPage()
	if !strings.Contains(page, "EventSource") {
		t.Fatal("should use EventSource for real-time updates")
	}
	if !strings.Contains(page, "/__mdp/events") {
		t.Fatal("should connect to /__mdp/events SSE endpoint")
	}
}

func TestRenderSwitchPageNoReload(t *testing.T) {
	page := renderSwitchPage()
	if strings.Contains(page, "location.reload()") {
		t.Fatal("should not use location.reload()")
	}
}

func TestRenderSwitchPageThemeToggle(t *testing.T) {
	page := renderSwitchPage()
	for _, id := range []string{"theme-auto", "theme-light", "theme-dark"} {
		if !strings.Contains(page, id) {
			t.Fatalf("should contain theme button %q", id)
		}
	}
}

func TestSwitchPageHandler(t *testing.T) {
	handler := SwitchPageHandler()
	req := httptest.NewRequest(http.MethodGet, "/__mdp/switch", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
}
