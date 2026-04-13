package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderDashboardContainsTabs(t *testing.T) {
	page := renderDashboard(13100)
	for _, tab := range []string{"Groups", "Proxies", "Services", "Multiview"} {
		if !strings.Contains(page, tab) {
			t.Errorf("dashboard missing tab %q", tab)
		}
	}
}

func TestRenderDashboardFetchEndpoints(t *testing.T) {
	page := renderDashboard(13100)
	for _, ep := range []string{"/__mdp/proxies", "/__mdp/groups", "/__mdp/services"} {
		if !strings.Contains(page, ep) {
			t.Errorf("dashboard missing fetch endpoint %q", ep)
		}
	}
}

func TestRenderDashboardControlPort(t *testing.T) {
	page := renderDashboard(9999)
	if !strings.Contains(page, "http://localhost:9999") {
		t.Error("dashboard does not contain the control API URL with the given port")
	}
}

func TestRenderDashboardThemeToggle(t *testing.T) {
	page := renderDashboard(13100)
	for _, id := range []string{"theme-auto", "theme-light", "theme-dark"} {
		if !strings.Contains(page, id) {
			t.Errorf("dashboard missing theme button %q", id)
		}
	}
}

func TestRenderDashboardValidHTML(t *testing.T) {
	page := renderDashboard(13100)
	if !strings.HasPrefix(page, "<!DOCTYPE html>") {
		t.Error("dashboard does not start with <!DOCTYPE html>")
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Error("dashboard does not end with </html>")
	}
}

func TestDashboardHandler(t *testing.T) {
	handler := DashboardHandler(13100)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html content type, got %q", ct)
	}
}
