package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStateKey(t *testing.T) {
	tests := []struct {
		repo, group, want string
	}{
		{"acme/web", "main", "acme-web_main"},
		{"acme/web", "feature/login", "acme-web_feature-login"},
		{"repo with spaces", "grp", "repo-with-spaces_grp"},
		{"", "", "default"},
		{"a/b/c", "x", "a-b-c_x"},
	}
	for _, tt := range tests {
		if got := runStateKey(tt.repo, tt.group); got != tt.want {
			t.Errorf("runStateKey(%q, %q) = %q, want %q", tt.repo, tt.group, got, tt.want)
		}
	}
}

func TestRunStateKeyNoCollision(t *testing.T) {
	// The repo/group join must not be ambiguous with content dashes.
	if a, b := runStateKey("a-b", "c"), runStateKey("a", "b-c"); a == b {
		t.Errorf("distinct repo/group pairs collided: both produced %q", a)
	}
}

func TestRunStateKeyFilesystemSafe(t *testing.T) {
	key := runStateKey("acme/web", "feature/x")
	if strings.ContainsAny(key, `/\ `) {
		t.Errorf("runStateKey produced unsafe filename component: %q", key)
	}
}

func TestDecodeDetachedInputsRoundTrip(t *testing.T) {
	want := map[string]string{"env": "staging", "token": "abc=123"}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("_MDP_RUN_INPUTS", string(encoded))

	got, err := decodeDetachedInputs()
	if err != nil {
		t.Fatalf("decodeDetachedInputs: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("input %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestDecodeDetachedInputsEmpty(t *testing.T) {
	for _, raw := range []string{"", "null"} {
		t.Setenv("_MDP_RUN_INPUTS", raw)
		got, err := decodeDetachedInputs()
		if err != nil {
			t.Fatalf("decodeDetachedInputs(%q): %v", raw, err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("decodeDetachedInputs(%q) = %v, want empty non-nil map", raw, got)
		}
	}
}

func TestRunBatchStopNoRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "mdp.yaml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	err := runBatchStop(13100, "main")
	if err == nil || !strings.Contains(err.Error(), "no detached run") {
		t.Fatalf("expected 'no detached run' error, got %v", err)
	}
}
