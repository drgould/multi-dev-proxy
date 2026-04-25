package envexport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

func TestWritePerServiceSortsAndEscapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	env := []string{
		`ZETA=last`,
		`ALPHA=first`,
		`WITH_QUOTE=say "hi"`,
		`WITH_NEWLINE=line1` + "\n" + `line2`,
		`WITH_BACKSLASH=a\b`,
		`EMPTY=`,
	}
	if err := WritePerService(path, env); err != nil {
		t.Fatalf("WritePerService: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	want := header +
		"ALPHA=\"first\"\n" +
		"EMPTY=\"\"\n" +
		"WITH_BACKSLASH=\"a\\\\b\"\n" +
		"WITH_NEWLINE=\"line1\\nline2\"\n" +
		"WITH_QUOTE=\"say \\\"hi\\\"\"\n" +
		"ZETA=\"last\"\n"
	if got != want {
		t.Errorf("contents mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestWritePerServicePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", ".env")
	if err := WritePerService(path, []string{"FOO=bar"}); err != nil {
		t.Fatalf("WritePerService: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}
}

func TestWriteGlobalResolvesRefsAndInterpolations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	pm := envexpand.PortMap{"api": {"PORT": 8080}, "db": {"DB_PORT": 5432}}
	em := envexpand.EnvMap{
		"api": {"PORT": "8080", "NAME": "api"},
		"db":  {"DB_PORT": "5432"},
	}
	global := map[string]config.EnvValue{
		"API_PORT": {Ref: "api.env.PORT"},
		"DB_PORT":  {Ref: "db.env.DB_PORT"},
		"API_URL":  {Value: "http://localhost:${api.PORT}"},
		"STATIC":   {Value: "hello world"},
	}
	if err := WriteGlobal(path, global, pm, em); err != nil {
		t.Fatalf("WriteGlobal: %v", err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, line := range []string{
		`API_PORT="8080"`,
		`API_URL="http://localhost:8080"`,
		`DB_PORT="5432"`,
		`STATIC="hello world"`,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("missing line %q in:\n%s", line, got)
		}
	}
}

func TestWriteGlobalFailsOnUnresolvedRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	global := map[string]config.EnvValue{
		"FOO": {Ref: "nope.env.MISSING"},
	}
	err := WriteGlobal(path, global, envexpand.PortMap{}, envexpand.EnvMap{})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not have been written on error")
	}
}

func TestWriteGlobalFailsOnBadInterpolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	global := map[string]config.EnvValue{
		"FOO": {Value: "${nope.port}"},
	}
	err := WriteGlobal(path, global, envexpand.PortMap{}, envexpand.EnvMap{})
	if err == nil {
		t.Fatal("expected error")
	}
}
