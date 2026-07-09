package orchestrator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

func setupLogsAPI(t *testing.T) (string, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	o := New(&config.Config{}, "", "")
	capi := NewControlAPI(o, nil)
	capi.logDir = dir
	return dir, capi.Handler()
}

func writeLog(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

type tailResponse struct {
	Lines      []string `json:"lines"`
	NextOffset int64    `json:"nextOffset"`
	Size       int64    `json:"size"`
	Truncated  bool     `json:"truncated"`
}

func getTail(t *testing.T, handler http.Handler, url string) (int, tailResponse) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body tailResponse
	_ = json.NewDecoder(rec.Body).Decode(&body)
	return rec.Code, body
}

func TestLogsListSources(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "daemon line\n")
	writeLog(t, dir, "run-myrepo_dev.log", "run line\n")
	writeLog(t, dir, "unrelated.log", "ignored\n")

	req := httptest.NewRequest("GET", "/__mdp/logs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var sources []logSourceInfo
	if err := json.NewDecoder(rec.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %+v", sources)
	}
	if sources[0].ID != "daemon" || sources[1].ID != "run-myrepo_dev" {
		t.Errorf("unexpected sources: %+v", sources)
	}
	if sources[1].Label != "run myrepo_dev" {
		t.Errorf("unexpected run label: %q", sources[1].Label)
	}
	if sources[0].SizeBytes != int64(len("daemon line\n")) {
		t.Errorf("unexpected size: %d", sources[0].SizeBytes)
	}
}

func TestLogsListEmpty(t *testing.T) {
	_, handler := setupLogsAPI(t)
	req := httptest.NewRequest("GET", "/__mdp/logs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var sources []logSourceInfo
	if err := json.NewDecoder(rec.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Errorf("expected no sources, got %+v", sources)
	}
}

func TestLogsTailFull(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	content := "one\ntwo\nthree\n"
	writeLog(t, dir, "orchestrator.log", content)

	code, body := getTail(t, handler, "/__mdp/logs/daemon")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 3 || body.Lines[0] != "one" || body.Lines[2] != "three" {
		t.Errorf("unexpected lines: %+v", body.Lines)
	}
	if body.NextOffset != int64(len(content)) {
		t.Errorf("expected nextOffset %d, got %d", len(content), body.NextOffset)
	}
	if body.Truncated {
		t.Error("full read should not be truncated")
	}
}

func TestLogsTailNegativeOffsetSkipsPartialLine(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "first\nsecond\nthird\n")

	// -9 lands inside "second\n" — the partial line must be skipped.
	code, body := getTail(t, handler, "/__mdp/logs/daemon?offset=-9")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 1 || body.Lines[0] != "third" {
		t.Errorf("expected only [third], got %+v", body.Lines)
	}
}

func TestLogsTailHoldsIncompleteTrailingLine(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "one\ntwo\npartial")

	code, body := getTail(t, handler, "/__mdp/logs/daemon?offset=0")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 2 || body.Lines[1] != "two" {
		t.Fatalf("incomplete trailing line must be held back, got %+v", body.Lines)
	}
	if body.NextOffset != int64(len("one\ntwo\n")) {
		t.Errorf("cursor must stop before the partial line, got %d", body.NextOffset)
	}

	// Once the line is newline-terminated it appears exactly once.
	f, _ := os.OpenFile(filepath.Join(dir, "orchestrator.log"), os.O_APPEND|os.O_WRONLY, 0644)
	fmt.Fprint(f, "\n")
	f.Close()
	code, body = getTail(t, handler, fmt.Sprintf("/__mdp/logs/daemon?offset=%d", body.NextOffset))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 1 || body.Lines[0] != "partial" {
		t.Errorf("expected [partial] once completed, got %+v", body.Lines)
	}
}

func TestLogsTailNegativeOffsetLineAligned(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "aaa\nbbb\nccc\n") // 12 bytes

	// -8 lands exactly on the start of "bbb"; no partial line to skip.
	code, body := getTail(t, handler, "/__mdp/logs/daemon?offset=-8")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 2 || body.Lines[0] != "bbb" || body.Lines[1] != "ccc" {
		t.Errorf("newline-aligned read must keep the first line, got %+v", body.Lines)
	}
}

func TestLogsTailNegativeOffsetBeyondStart(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "one\ntwo\n")

	code, body := getTail(t, handler, "/__mdp/logs/daemon?offset=-100000")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 2 {
		t.Errorf("oversized negative offset should read from start, got %+v", body.Lines)
	}
}

func TestLogsTailCursorPagination(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "aaaa\nbbbb\ncccc\n")

	// limit 7 covers "aaaa\nbb" — must cut back to the newline.
	code, body := getTail(t, handler, "/__mdp/logs/daemon?offset=0&limit=7")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 1 || body.Lines[0] != "aaaa" {
		t.Errorf("expected whole-line cut [aaaa], got %+v", body.Lines)
	}
	if !body.Truncated {
		t.Error("expected truncated=true")
	}

	code, body = getTail(t, handler, fmt.Sprintf("/__mdp/logs/daemon?offset=%d", body.NextOffset))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(body.Lines) != 2 || body.Lines[0] != "bbbb" || body.Lines[1] != "cccc" {
		t.Errorf("expected remainder [bbbb cccc], got %+v", body.Lines)
	}
	if body.Truncated {
		t.Error("expected truncated=false at EOF")
	}
}

func TestLogsTailGrowth(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "old\n")

	_, body := getTail(t, handler, "/__mdp/logs/daemon")
	cursor := body.NextOffset

	f, err := os.OpenFile(filepath.Join(dir, "orchestrator.log"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "new\n")
	f.Close()

	_, body = getTail(t, handler, fmt.Sprintf("/__mdp/logs/daemon?offset=%d", cursor))
	if len(body.Lines) != 1 || body.Lines[0] != "new" {
		t.Errorf("expected only new lines, got %+v", body.Lines)
	}
}

func TestLogsTailUnknownID(t *testing.T) {
	dir, handler := setupLogsAPI(t)
	writeLog(t, dir, "orchestrator.log", "x\n")

	for _, id := range []string{"bogus", "run-..%2Fsecret", "..%2Forchestrator"} {
		code, _ := getTail(t, handler, "/__mdp/logs/"+id)
		if code != http.StatusNotFound {
			t.Errorf("id %q: expected 404, got %d", id, code)
		}
	}
}

func TestLogsTailMissingFile(t *testing.T) {
	_, handler := setupLogsAPI(t)
	code, _ := getTail(t, handler, "/__mdp/logs/daemon")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for missing log, got %d", code)
	}
}
