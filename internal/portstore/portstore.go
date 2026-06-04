// Package portstore persists per-(repo, group) port assignments under ~/.mdp
// so a branch's services keep the same ports across mdp restarts. Reuse is
// best-effort — callers only reuse a remembered port when it is still free, so
// a missing or stale file never blocks a run.
package portstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

// dir returns the directory where port-assignment files live. Declared as a
// var so tests can redirect it without touching $HOME.
var dir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mdp", "ports")
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileName builds the JSON filename for a (repo, group) pair. The human-readable
// parts are sanitized for the filesystem and suffixed with a short hash of the
// raw inputs so distinct pairs that sanitize to the same string don't collide.
func fileName(repo, group string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + group))
	h := hex.EncodeToString(sum[:])[:8]
	return sanitize(repo) + "__" + sanitize(group) + "__" + h + ".json"
}

func sanitize(s string) string {
	s = unsafeChars.ReplaceAllString(s, "-")
	if s == "" {
		return "_"
	}
	return s
}

// Load returns the remembered port assignments for (repo, group). Any error
// (missing file, unreadable, malformed) yields an empty map so callers can
// treat persistence as advisory.
func Load(repo, group string) map[string]int {
	data, err := os.ReadFile(filepath.Join(dir(), fileName(repo, group)))
	if err != nil {
		return map[string]int{}
	}
	m := map[string]int{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]int{}
	}
	return m
}

// Save writes the port assignments for (repo, group) atomically (temp file +
// rename). encoding/json emits map keys in sorted order, so output is stable.
func Save(repo, group string, m map[string]int) error {
	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(d, fileName(repo, group))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
