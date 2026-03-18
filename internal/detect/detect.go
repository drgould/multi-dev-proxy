package detect

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// remoteURLRepoPattern matches the repo name from HTTPS or SSH remote URLs.
// HTTPS: https://github.com/user/repo.git → repo
// SSH:   git@github.com:user/repo.git     → repo
var remoteURLRepoPattern = regexp.MustCompile(`[/:]([^/]+?)(?:\.git)?$`)

// DetectRepo detects the repository name from the git remote of the given directory.
// Fallback chain:
//  1. git remote get-url origin → parse repo name
//  2. basename of dir
func DetectRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err == nil {
		remoteURL := strings.TrimSpace(string(out))
		if m := remoteURLRepoPattern.FindStringSubmatch(remoteURL); m != nil {
			return m[1]
		}
	}
	// Fallback: directory basename
	return filepath.Base(dir)
}

// DetectBranch returns the current git branch name in the given directory.
// Falls back to "unknown" if git is unavailable or HEAD is detached.
func DetectBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "unknown"
	}
	return branch
}

// ServerName combines repo and branch into the canonical server name format.
func ServerName(repo, branch string) string {
	return repo + "/" + branch
}

// DetectServerName detects and returns the server name for the given directory.
func DetectServerName(dir string) string {
	return ServerName(DetectRepo(dir), DetectBranch(dir))
}
