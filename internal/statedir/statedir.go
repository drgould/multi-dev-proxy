// Package statedir resolves the paths of mdp's on-disk state (~/.mdp),
// shared between the CLI (which writes the files) and the orchestrator
// (which serves log files over the control API).
package statedir

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Dir returns the mdp state directory (~/.mdp).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mdp")
}

// PIDFile is where the daemon spawner records the orchestrator PID.
func PIDFile() string {
	return filepath.Join(Dir(), "orchestrator.pid")
}

// LogFile is where the daemon's stdout/stderr are redirected by the
// spawning process.
func LogFile() string {
	return filepath.Join(Dir(), "orchestrator.log")
}

// slugRe collapses any run of non-alphanumeric characters into a single
// dash, so a slugged component contains only [A-Za-z0-9-].
var slugRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slugComponent(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
}

// RunKey builds a filesystem-safe slug identifying a detached batch run by
// its repo and group, so `mdp run --stop` can find the same PID file the
// detaching parent wrote. repo and group are slugged separately and joined
// with "_" — which can't appear inside a slugged component — so distinct
// pairs like ("a-b","c") and ("a","b-c") never collide onto one state file.
func RunKey(repo, group string) string {
	key := slugComponent(repo) + "_" + slugComponent(group)
	if key == "_" {
		key = "default"
	}
	return key
}

// RunPIDFile is where a detached batch run records its supervisor PID.
func RunPIDFile(repo, group string) string {
	return filepath.Join(Dir(), "run-"+RunKey(repo, group)+".pid")
}

// RunLogFile is where a detached batch run's combined stdout/stderr is
// written.
func RunLogFile(repo, group string) string {
	return filepath.Join(Dir(), "run-"+RunKey(repo, group)+".log")
}
