package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWidgetHandler(t *testing.T) {
	handler := WidgetHandler()
	req := httptest.NewRequest(http.MethodGet, "/__mdp/widget.js", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/javascript; charset=utf-8")
	}

	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "attachShadow") {
		t.Error("response body missing attachShadow")
	}
}

func TestWidgetJSContainsShadowDOM(t *testing.T) {
	if !strings.Contains(WidgetJS, `attachShadow({ mode: "open" })`) &&
		!strings.Contains(WidgetJS, "attachShadow({ mode: 'open' })") {
		t.Error("WidgetJS missing attachShadow with mode open")
	}
}

func TestWidgetJSFetchesAPI(t *testing.T) {
	if !strings.Contains(WidgetJS, "/__mdp/servers") {
		t.Error("WidgetJS missing /__mdp/servers API endpoint")
	}
}

func TestWidgetJSSetsCookie(t *testing.T) {
	if !strings.Contains(WidgetJS, "__mdp_upstream") {
		t.Error("WidgetJS missing __mdp_upstream cookie")
	}
}

func TestWidgetJSReloads(t *testing.T) {
	if !strings.Contains(WidgetJS, "location.reload") {
		t.Error("WidgetJS missing location.reload call")
	}
}

func TestWidgetJSPillShowsRepoAndBranch(t *testing.T) {
	if !strings.Contains(WidgetJS, "function pillLabel(") {
		t.Error("WidgetJS missing pillLabel for repo · branch pill")
	}
}
