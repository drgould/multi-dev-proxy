package orchestrator

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// logSourceInfo describes one log file served by the control API.
type logSourceInfo struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	SizeBytes int64     `json:"sizeBytes"`
	ModTime   time.Time `json:"modTime"`
}

const (
	logTailDefaultLimit = 64 * 1024
	logTailMaxLimit     = 1024 * 1024
)

// validLogID whitelists ids of the shape handleListLogs enumerates; ids are
// never client-supplied paths.
var validLogID = regexp.MustCompile(`^(daemon|run-[A-Za-z0-9_-]+)$`)

func (c *ControlAPI) logPath(id string) string {
	if id == "daemon" {
		return filepath.Join(c.logDir, "orchestrator.log")
	}
	return filepath.Join(c.logDir, id+".log")
}

// handleListLogs returns the log sources the daemon can serve: its own log
// plus any detached-run logs found in the state dir.
func (c *ControlAPI) handleListLogs(w http.ResponseWriter, r *http.Request) {
	sources := []logSourceInfo{}
	if fi, err := os.Stat(c.logPath("daemon")); err == nil {
		sources = append(sources, logSourceInfo{ID: "daemon", Label: "daemon", SizeBytes: fi.Size(), ModTime: fi.ModTime()})
	}
	matches, _ := filepath.Glob(filepath.Join(c.logDir, "run-*.log"))
	sort.Strings(matches)
	for _, path := range matches {
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".log")
		if !validLogID.MatchString(id) {
			continue
		}
		sources = append(sources, logSourceInfo{
			ID:        id,
			Label:     "run " + strings.TrimPrefix(id, "run-"),
			SizeBytes: fi.Size(),
			ModTime:   fi.ModTime(),
		})
	}
	writeJSON(w, http.StatusOK, sources)
}

// handleTailLog serves a byte-offset cursor read of one log file.
// offset < 0 means "the last |offset| bytes" (a partial leading line is
// skipped unless the read lands on a line boundary); an incomplete trailing
// line is held back. The response's nextOffset is the cursor for the next poll.
func (c *ControlAPI) handleTailLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validLogID.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown log id"})
		return
	}
	f, err := os.Open(c.logPath(id))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	size := fi.Size()

	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit <= 0 {
		limit = logTailDefaultLimit
	}
	if limit > logTailMaxLimit {
		limit = logTailMaxLimit
	}
	skipPartialFirst := false
	if offset < 0 {
		offset += size
		if offset <= 0 {
			offset = 0
		} else {
			// Landing mid-file: skip a partial leading line — unless we landed
			// exactly on a line boundary, in which case the first line is whole.
			var one [1]byte
			if _, e := f.ReadAt(one[:], offset-1); e == nil && one[0] != '\n' {
				skipPartialFirst = true
			}
		}
	}
	if offset > size {
		offset = size
	}

	buf := make([]byte, limit)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	chunk := buf[:n]
	consumed := int64(n)
	truncated := offset+consumed < size

	// Hold back an incomplete trailing line: cut at the last newline so a line
	// still being written isn't emitted (then re-emitted) as a fragment. With
	// no newline at all, emit nothing at EOF; if more data exists (a single
	// line longer than the read limit) emit the raw chunk so the cursor can't
	// stall.
	if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
		chunk = chunk[:i+1]
		consumed = int64(i + 1)
	} else if !truncated {
		chunk = nil
		consumed = 0
	}

	if skipPartialFirst {
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			chunk = chunk[i+1:]
		} else {
			chunk = nil
		}
	}

	lines := []string{}
	if s := strings.TrimSuffix(string(chunk), "\n"); s != "" {
		lines = strings.Split(s, "\n")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lines":      lines,
		"nextOffset": offset + consumed,
		"size":       size,
		"truncated":  truncated,
	})
}
