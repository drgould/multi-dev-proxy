package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

// runStateSlug collapses any run of non-alphanumeric characters into a single
// dash, so a slugged component contains only [A-Za-z0-9-].
var runStateSlug = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slugComponent(s string) string {
	return strings.Trim(runStateSlug.ReplaceAllString(s, "-"), "-")
}

// runStateKey builds a filesystem-safe slug identifying a detached batch run by
// its repo and group, so `mdp run --stop` can find the same PID file the
// detaching parent wrote. repo and group are slugged separately and joined with
// "_" — which can't appear inside a slugged component — so distinct pairs like
// ("a-b","c") and ("a","b-c") never collide onto one state file.
func runStateKey(repo, group string) string {
	key := slugComponent(repo) + "_" + slugComponent(group)
	if key == "_" {
		key = "default"
	}
	return key
}

// runPIDFilePath is where a detached batch run records its supervisor PID.
func runPIDFilePath(repo, group string) string {
	return filepath.Join(stateDir(), "run-"+runStateKey(repo, group)+".pid")
}

// runLogFilePath is where a detached batch run's combined stdout/stderr is written.
func runLogFilePath(repo, group string) string {
	return filepath.Join(stateDir(), "run-"+runStateKey(repo, group)+".log")
}
