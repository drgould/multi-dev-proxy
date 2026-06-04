package portstore

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempDir redirects the store directory to a fresh temp dir for the test.
func withTempDir(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	orig := dir
	dir = func() string { return tmp }
	t.Cleanup(func() { dir = orig })
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempDir(t)

	want := map[string]int{"api": 12345, "web.PORT": 23456}
	if err := Save("myrepo", "feature/x", want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load("myrepo", "feature/x")
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %d, want %d", k, got[k], v)
		}
	}
}

func TestLoadMergeSavePreservesOtherKeys(t *testing.T) {
	withTempDir(t)

	// A batch run records two services.
	if err := Save("repo", "main", map[string]int{"api": 100, "web": 200}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A later (e.g. single-command) run reuses Load → merge → Save with a
	// disjoint key. The original entries must survive.
	remembered := Load("repo", "main")
	for k, v := range map[string]int{"adhoc": 300} {
		remembered[k] = v
	}
	if err := Save("repo", "main", remembered); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load("repo", "main")
	want := map[string]int{"api": 100, "web": 200, "adhoc": 300}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %d, want %d", k, got[k], v)
		}
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	withTempDir(t)

	got := Load("norepo", "nobranch")
	if got == nil {
		t.Fatal("Load returned nil; want empty map")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestLoadMalformedReturnsEmpty(t *testing.T) {
	withTempDir(t)

	path := filepath.Join(dir(), fileName("repo", "branch"))
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Load("repo", "branch"); len(got) != 0 {
		t.Errorf("got %v, want empty map", got)
	}
}

func TestFileNameSanitizesAndDisambiguates(t *testing.T) {
	// Branch names with slashes must not produce nested paths.
	name := fileName("myrepo", "feature/login")
	if filepath.Base(name) != name {
		t.Errorf("fileName produced a path separator: %q", name)
	}

	// Distinct pairs that sanitize to the same readable string must differ
	// thanks to the hash suffix.
	a := fileName("repo", "a/b")
	b := fileName("repo", "a-b")
	if a == b {
		t.Errorf("distinct pairs collided: %q == %q", a, b)
	}

	// Same inputs are stable across calls.
	if fileName("repo", "a/b") != a {
		t.Error("fileName not deterministic for the same inputs")
	}
}
