package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadAllLines(t *testing.T) {
	f, err := os.CreateTemp("", "mdp-log-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	content := "line1\nline2\nline3\nline4\nline5\n"
	f.WriteString(content)

	lines, err := readAllLines(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	if lines[0] != "line1" {
		t.Errorf("expected first line 'line1', got %q", lines[0])
	}
	if lines[4] != "line5" {
		t.Errorf("expected last line 'line5', got %q", lines[4])
	}
}

func TestReadAllLinesEmpty(t *testing.T) {
	f, err := os.CreateTemp("", "mdp-log-empty")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	lines, err := readAllLines(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
}

func TestTailLinesAll(t *testing.T) {
	f, err := os.CreateTemp("", "mdp-log-tail")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	for i := 1; i <= 10; i++ {
		f.WriteString("line\n")
	}
	f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = tailLines(f.Name(), 0)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatal(err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	out := string(buf[:n])

	count := strings.Count(out, "line")
	if count != 10 {
		t.Errorf("n=0 should return all lines, got %d", count)
	}
}

func TestTailLinesLast3(t *testing.T) {
	f, err := os.CreateTemp("", "mdp-log-tail3")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	for i := 1; i <= 10; i++ {
		f.WriteString(strings.Repeat("x", i) + "\n")
	}
	f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = tailLines(f.Name(), 3)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatal(err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	out := string(buf[:n])
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != strings.Repeat("x", 8) {
		t.Errorf("expected 8 x's on first tail line, got %q", lines[0])
	}
}

func TestTailLinesMissingFile(t *testing.T) {
	err := tailLines("/nonexistent/path/mdp.log", 10)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTailLinesMoreThanAvailable(t *testing.T) {
	f, err := os.CreateTemp("", "mdp-log-fewer")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("a\nb\n")
	f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = tailLines(f.Name(), 100)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatal(err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	out := string(buf[:n])
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines when requesting more than available, got %d", len(lines))
	}
}

func TestRunLogsMissingFile(t *testing.T) {
	cmd := &cobra.Command{Use: "logs", RunE: runLogs}
	cmd.Flags().BoolP("follow", "f", false, "")
	cmd.Flags().IntP("lines", "n", 50, "")

	// runLogs checks logFilePath() which points to a platform-specific path.
	// On a clean test env, the log file likely doesn't exist.
	err := cmd.Execute()
	// Either succeeds (file exists) or fails with "no log file found"
	if err != nil && !strings.Contains(err.Error(), "no log file found") {
		t.Errorf("unexpected error: %v", err)
	}
}
