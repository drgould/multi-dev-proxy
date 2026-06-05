package portstore

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

func TestSaveMergesWithExistingFile(t *testing.T) {
	withTempDir(t)

	// A batch run records two services.
	if err := Save("repo", "main", map[string]int{"api": 100, "web": 200}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A later run (e.g. a disjoint `--service` subset, or single-command mode)
	// saves only its own key. Save merges, so the originals survive.
	if err := Save("repo", "main", map[string]int{"adhoc": 300}); err != nil {
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

func TestConcurrentSavesDoNotDropEntries(t *testing.T) {
	withTempDir(t)

	// Many processes/goroutines saving disjoint keys to the same (repo, group)
	// file concurrently must not drop each other's entries. The locked
	// read-merge-write in Save serializes them; without it this loses entries.
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "svc" + strconv.Itoa(i)
			if err := Save("repo", "main", map[string]int{key: 10000 + i}); err != nil {
				t.Errorf("Save: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got := Load("repo", "main")
	if len(got) != n {
		t.Fatalf("got %d entries, want %d — concurrent saves dropped entries: %v", len(got), n, got)
	}
	for i := 0; i < n; i++ {
		key := "svc" + strconv.Itoa(i)
		if got[key] != 10000+i {
			t.Errorf("key %q: got %d, want %d", key, got[key], 10000+i)
		}
	}
}

func TestSaveSkipsWhenNoHomeDir(t *testing.T) {
	// dir() returns "" when the home directory is unknown; Save must be a no-op
	// rather than writing a relative ".mdp/" into the cwd.
	orig := dir
	dir = func() string { return "" }
	t.Cleanup(func() { dir = orig })

	if err := Save("repo", "main", map[string]int{"api": 1}); err != nil {
		t.Errorf("Save with no home dir should be a no-op, got: %v", err)
	}
	if got := Load("repo", "main"); len(got) != 0 {
		t.Errorf("Load with no home dir should be empty, got %v", got)
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
