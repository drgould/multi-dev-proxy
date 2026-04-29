package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

// peerRef identifies one cross-repo @-reference: a (repo, svc, key) tuple
// looked up against the orchestrator's registry, plus the rendered "current"
// value from the most recent lookup. Used by the peer-watcher to detect
// changes.
type peerRef struct {
	repo    string
	svc     string
	isEnv   bool
	key     string
	current string
	found   bool
}

func (r peerRef) signature() string {
	t := "port"
	if r.isEnv {
		t = "env"
	}
	return fmt.Sprintf("@%s.%s.%s.%s", r.repo, r.svc, t, r.key)
}

// extractPeerRefs returns every distinct @-reference appearing in the given
// service's env entries. The current/found fields are zero-valued; populate
// them via resolvePeer.
func extractPeerRefs(svc config.ServiceConfig) []peerRef {
	seen := map[string]peerRef{}

	add := func(c envexpand.CrossRepoRef) {
		p := peerRef{repo: c.Repo, svc: c.Svc, isEnv: c.IsEnv, key: c.Key}
		seen[p.signature()] = p
	}

	for _, entry := range svc.Env {
		if entry.Ref != "" {
			if c, ok := envexpand.ParseCrossRepoBareRef(entry.Ref); ok {
				add(c)
			}
			continue
		}
		for _, c := range envexpand.ScanCrossRepoRefs(entry.Value) {
			add(c)
		}
	}

	out := make([]peerRef, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].signature() < out[j].signature() })
	return out
}

// effectiveGroup returns the group to query for a given peer repo. linkMap
// overrides the caller's group on a per-repo basis when --link <repo>=<group>
// was passed at startup.
func effectiveGroup(repo, defaultGroup string, linkMap map[string]string) string {
	if g, ok := linkMap[repo]; ok && g != "" {
		return g
	}
	return defaultGroup
}

// resolvePeer queries the orchestrator for one peer reference. Returns
// (value, true) when the peer is registered and the requested key resolves;
// (zero, false) otherwise. defaultGroup is the caller's group; linkMap
// (optional, may be nil) overrides the lookup group for specific peer repos.
func resolvePeer(client *http.Client, controlURL, defaultGroup string, linkMap map[string]string, ref peerRef) (string, bool) {
	q := url.Values{}
	q.Set("group", effectiveGroup(ref.repo, defaultGroup, linkMap))
	q.Set("repo", ref.repo)
	q.Set("svc", ref.svc) // unused by current handler, kept for future indexing
	q.Set("service", ref.svc)
	resp, err := client.Get(controlURL + "/__mdp/peers?" + q.Encode())
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Port int               `json:"port"`
		Env  map[string]string `json:"env"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	if ref.isEnv {
		val, ok := body.Env[ref.key]
		return val, ok
	}
	if body.Port == 0 {
		return "", false
	}
	return strconv.Itoa(body.Port), true
}

// newPeerResolver returns an envexpand.Resolver bound to one (group, controlURL)
// pair. linkMap (optional) routes cross-repo refs to a different group on a
// per-repo basis. It is safe to call concurrently.
func newPeerResolver(client *http.Client, controlURL, group string, linkMap map[string]string) envexpand.Resolver {
	return func(repo, svc string, isEnv bool, key string) (string, bool) {
		return resolvePeer(client, controlURL, group, linkMap, peerRef{repo: repo, svc: svc, isEnv: isEnv, key: key})
	}
}

// refreshPeerRefs queries the orchestrator for every ref and writes the
// current/found fields. Returns whether any value changed since the last
// refresh. linkMap (optional) overrides the lookup group per peer repo.
func refreshPeerRefs(client *http.Client, controlURL, defaultGroup string, linkMap map[string]string, refs []peerRef) (changed bool, updated []peerRef) {
	updated = make([]peerRef, len(refs))
	for i, r := range refs {
		val, ok := resolvePeer(client, controlURL, defaultGroup, linkMap, r)
		if val != r.current || ok != r.found {
			changed = true
		}
		r.current = val
		r.found = ok
		updated[i] = r
	}
	return changed, updated
}

// watchPeerRefs polls the orchestrator on interval. When ANY watched ref's
// resolved value changes, it sends on changed once and exits. The caller
// re-invokes the watcher after restarting the dependent service. linkMap
// (optional) overrides the lookup group per peer repo.
func watchPeerRefs(ctx context.Context, client *http.Client, controlURL, defaultGroup string, linkMap map[string]string, refs []peerRef, interval time.Duration, changed chan<- []peerRef) {
	if len(refs) == 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	current := refs
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			didChange, next := refreshPeerRefs(client, controlURL, defaultGroup, linkMap, current)
			if didChange {
				slog.Info("peer state changed", "refs", peerRefSignatures(next))
				select {
				case changed <- next:
				case <-ctx.Done():
				}
				return
			}
			current = next
		}
	}
}

func peerRefSignatures(refs []peerRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.signature()
	}
	return out
}
