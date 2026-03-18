package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupGitRepo(t *testing.T, dir string) {
	t.Helper()
	// Initialize git repo
	if err := exec.Command("git", "-C", dir, "init").Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	// Configure git user
	if err := exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run(); err != nil {
		t.Fatalf("git config user.email failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "config", "user.name", "Test").Run(); err != nil {
		t.Fatalf("git config user.name failed: %v", err)
	}
	// Create initial commit
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "commit", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
}

func TestDetectRepoHTTPS(t *testing.T) {
	dir := t.TempDir()
	setupGitRepo(t, dir)

	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/user/myrepo.git").Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	repo := DetectRepo(dir)
	if repo != "myrepo" {
		t.Errorf("DetectRepo() = %q, want %q", repo, "myrepo")
	}
}

func TestDetectRepoSSH(t *testing.T) {
	dir := t.TempDir()
	setupGitRepo(t, dir)

	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", "git@github.com:user/myrepo.git").Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	repo := DetectRepo(dir)
	if repo != "myrepo" {
		t.Errorf("DetectRepo() = %q, want %q", repo, "myrepo")
	}
}

func TestDetectRepoNoRemote(t *testing.T) {
	dir := t.TempDir()
	setupGitRepo(t, dir)

	// No remote added, should fall back to directory basename
	repo := DetectRepo(dir)
	expected := filepath.Base(dir)
	if repo != expected {
		t.Errorf("DetectRepo() = %q, want %q", repo, expected)
	}
}

func TestDetectRepoNonGit(t *testing.T) {
	dir := t.TempDir()
	// No git initialization, plain directory

	repo := DetectRepo(dir)
	expected := filepath.Base(dir)
	if repo != expected {
		t.Errorf("DetectRepo() = %q, want %q", repo, expected)
	}
}

func TestDetectBranch(t *testing.T) {
	dir := t.TempDir()
	setupGitRepo(t, dir)

	// Create and checkout a new branch
	if err := exec.Command("git", "-C", dir, "checkout", "-b", "feature-xyz").Run(); err != nil {
		t.Fatalf("git checkout failed: %v", err)
	}

	branch := DetectBranch(dir)
	if branch != "feature-xyz" {
		t.Errorf("DetectBranch() = %q, want %q", branch, "feature-xyz")
	}
}

func TestDetectBranchFallback(t *testing.T) {
	dir := t.TempDir()
	// No git initialization, plain directory

	branch := DetectBranch(dir)
	if branch != "unknown" {
		t.Errorf("DetectBranch() = %q, want %q", branch, "unknown")
	}
}

func TestServerName(t *testing.T) {
	result := ServerName("myrepo", "main")
	expected := "myrepo/main"
	if result != expected {
		t.Errorf("ServerName() = %q, want %q", result, expected)
	}
}

func TestDetectServerName(t *testing.T) {
	dir := t.TempDir()
	setupGitRepo(t, dir)

	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/user/testapp.git").Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	if err := exec.Command("git", "-C", dir, "checkout", "-b", "develop").Run(); err != nil {
		t.Fatalf("git checkout failed: %v", err)
	}

	serverName := DetectServerName(dir)
	expected := "testapp/develop"
	if serverName != expected {
		t.Errorf("DetectServerName() = %q, want %q", serverName, expected)
	}
}
