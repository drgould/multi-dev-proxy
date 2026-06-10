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

func TestWritePerServiceMergesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	existing := "# my notes\n" +
		"SECRET=keepme\n" +
		"PORT=1234\n" +
		"\n" +
		"  export NAME=old\n" +
		"UNRELATED=stays\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	env := []string{"PORT=40100", "NAME=api", "ZETA=new", "ALPHA=new"}
	if err := WritePerService(path, env); err != nil {
		t.Fatalf("WritePerService: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	want := "# my notes\n" +
		"SECRET=keepme\n" +
		"PORT=\"40100\"\n" +
		"\n" +
		"  export NAME=\"api\"\n" +
		"UNRELATED=stays\n" +
		"ALPHA=\"new\"\n" +
		"ZETA=\"new\"\n"
	if got != want {
		t.Errorf("contents mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestWritePerServiceMergeEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		env      []string
		want     string
	}{
		{
			name:     "inline comment preserved",
			existing: "PORT=1234 # dev port\n",
			env:      []string{"PORT=40100"},
			want:     "PORT=\"40100\" # dev port\n",
		},
		{
			name:     "comment after quoted value preserved",
			existing: "PORT=\"1234\" # dev port\n",
			env:      []string{"PORT=40100"},
			want:     "PORT=\"40100\" # dev port\n",
		},
		{
			name:     "spaces around equals",
			existing: "PORT = 1234\n",
			env:      []string{"PORT=40100"},
			want:     "PORT=\"40100\"\n",
		},
		{
			name:     "export with extra whitespace",
			existing: "export  PORT=1234\n",
			env:      []string{"PORT=40100"},
			want:     "export  PORT=\"40100\"\n",
		},
		{
			name:     "managed multi-line quoted value replaced whole",
			existing: "CERT=\"-----BEGIN-----\nabc\n-----END-----\"\nSECRET=keepme\n",
			env:      []string{"CERT=new"},
			want:     "CERT=\"new\"\nSECRET=keepme\n",
		},
		{
			name:     "unmanaged multi-line value not corrupted",
			existing: "CERT=\"-----BEGIN-----\nPORT=evil\n-----END-----\"\n",
			env:      []string{"PORT=40100"},
			want:     "CERT=\"-----BEGIN-----\nPORT=evil\n-----END-----\"\nPORT=\"40100\"\n",
		},
		{
			name:     "single-quoted multi-line value",
			existing: "NOTE='line1\nPORT=2' # tail\n",
			env:      []string{"NOTE=flat", "PORT=40100"},
			want:     "NOTE=\"flat\" # tail\nPORT=\"40100\"\n",
		},
		{
			name:     "commented-out assignment not matched",
			existing: "# PORT=1234\n#PORT=1234\n",
			env:      []string{"PORT=40100"},
			want:     "# PORT=1234\n#PORT=1234\nPORT=\"40100\"\n",
		},
		{
			name:     "BOM preserved and first key still matched",
			existing: "\xef\xbb\xbfPORT=1234\n",
			env:      []string{"PORT=40100"},
			want:     "\xef\xbb\xbfPORT=\"40100\"\n",
		},
		{
			name:     "unterminated quote consumed to EOF",
			existing: "PORT=\"1234\nno closing quote\n",
			env:      []string{"PORT=40100"},
			want:     "PORT=\"40100\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tt.existing), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := WritePerService(path, tt.env); err != nil {
				t.Fatalf("WritePerService: %v", err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != tt.want {
				t.Errorf("contents mismatch\n got: %q\nwant: %q", string(data), tt.want)
			}
		})
	}
}

func TestWritePerServiceMergeNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("SECRET=keepme"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WritePerService(path, []string{"PORT=8080"}); err != nil {
		t.Fatalf("WritePerService: %v", err)
	}
	data, _ := os.ReadFile(path)
	want := "SECRET=keepme\nPORT=\"8080\"\n"
	if string(data) != want {
		t.Errorf("contents mismatch\n got: %q\nwant: %q", string(data), want)
	}
}

func TestWritePerServiceMergePreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("SECRET=keepme\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WritePerService(path, []string{"PORT=8080"}); err != nil {
		t.Fatalf("WritePerService: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 0644", perm)
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
