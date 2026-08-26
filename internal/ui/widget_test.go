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

func TestWidgetJSTakesOverForeignServiceWorker(t *testing.T) {
	if !strings.Contains(WidgetJS, "getRegistrations") {
		t.Error("WidgetJS missing navigator.serviceWorker.getRegistrations() call")
	}
	if !strings.Contains(WidgetJS, ".unregister()") {
		t.Error("WidgetJS missing foreign Service Worker unregister() call")
	}
	if !strings.Contains(WidgetJS, "caches.keys()") || !strings.Contains(WidgetJS, "caches.delete(") {
		t.Error("WidgetJS missing Cache Storage cleanup alongside SW unregister")
	}
	// Bounce guard must be keyed by the fingerprint value itself, not a
	// bare one-shot flag — otherwise a later app change in the same tab
	// would silently skip its own reload.
	if !strings.Contains(WidgetJS, "__mdp_sw_bounced_for") {
		t.Error("WidgetJS bounce guard not keyed by servedBy fingerprint")
	}
	// getRegistrations() returns every registration for the origin, not
	// just root-scoped ones — takeover must filter to the "/" scope before
	// unregistering, or it deletes unrelated workers scoped to e.g. /docs/.
	if !strings.Contains(WidgetJS, "new URL(r.scope).pathname") {
		t.Error("WidgetJS foreign-SW filter not scoped to \"/\" before unregistering")
	}

	// The takeover block must run after the stale-pin bounce's
	// window.location.replace(...); return — never before it — so it can't
	// race that reload or fingerprint the wrong servedBy.
	bounceIdx := strings.Index(WidgetJS, "window.location.replace(bounce.toString())")
	takeoverIdx := strings.Index(WidgetJS, "Foreign Service Worker takeover")
	if bounceIdx == -1 || takeoverIdx == -1 {
		t.Fatal("could not locate stale-pin bounce or SW-takeover block in WidgetJS")
	}
	if takeoverIdx < bounceIdx {
		t.Error("SW-takeover block appears before the stale-pin bounce — it must run after, so it never races that reload")
	}
}
