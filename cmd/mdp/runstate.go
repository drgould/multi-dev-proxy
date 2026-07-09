package main

import "github.com/derekgould/multi-dev-proxy/internal/statedir"

// State paths live in internal/statedir so the orchestrator can serve log
// files over the control API; these delegates keep call sites short.

func stateDir() string { return statedir.Dir() }

func pidFilePath() string { return statedir.PIDFile() }

func logFilePath() string { return statedir.LogFile() }

func runStateKey(repo, group string) string { return statedir.RunKey(repo, group) }

// runPIDFilePath is where a detached batch run records its supervisor PID.
func runPIDFilePath(repo, group string) string { return statedir.RunPIDFile(repo, group) }

// runLogFilePath is where a detached batch run's combined stdout/stderr is written.
func runLogFilePath(repo, group string) string { return statedir.RunLogFile(repo, group) }
