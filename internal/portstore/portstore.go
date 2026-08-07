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

// dir returns the directory where port-assignment files live, or "" when the
// home directory can't be determined (e.g. $HOME unset in some containers/CI).
// Returning "" rather than a relative path keeps Save from leaking a `.mdp/`
// directory into the current working directory. Declared as a var so tests can
// redirect it without touching $HOME. Tests swap it without locking, so no
// test in this package may call t.Parallel() — doing so would race on it.
var dir = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
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

// readFile loads the assignment map at path, returning an empty map on any
// error (missing, unreadable, malformed) so persistence stays best-effort.
func readFile(path string) map[string]int {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]int{}
	}
	m := map[string]int{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]int{}
	}
	return m
}

// Load returns the remembered port assignments for (repo, group). Reads are
// lock-free: Save replaces the file via atomic rename, so a reader always sees
// a complete file (the old or the new one), never a torn write.
func Load(repo, group string) map[string]int {
	d := dir()
	if d == "" {
		return map[string]int{}
	}
	return readFile(filepath.Join(d, fileName(repo, group)))
}

// Save merges m into the persisted assignments for (repo, group) and writes the
// result atomically (temp file + rename). The read-merge-write runs under an
// exclusive file lock so concurrent mdp runs sharing the (repo, group) file —
// e.g. two `mdp run --service ...` invocations for disjoint subsets — don't drop
// each other's entries to a last-writer-wins overwrite.
func Save(repo, group string, m map[string]int) error {
	d := dir()
	if d == "" {
		return nil // no home dir — degrade to non-persistent (best-effort)
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	path := filepath.Join(d, fileName(repo, group))
	return withLock(path+".lock", func() error {
		merged := readFile(path)
		for k, v := range m {
			merged[k] = v
		}
		data, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return err
		}
		// Unique temp file + atomic rename. The lock already serializes writers;
		// CreateTemp guarantees the in-flight temp is never shared even so.
		tmp, err := os.CreateTemp(d, "ports-*.tmp")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return err
		}
		return nil
	})
}
