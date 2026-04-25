package envexpand

import (
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	pm := PortMap{
		"db":  {"port": 5432, "PORT": 5432},
		"api": {"PORT": 8080, "ADMIN": 8081},
	}

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"no refs", "hello", "hello", ""},
		{"single ref", "${db.port}", "5432", ""},
		{"embedded in url", "postgres://localhost:${db.port}/app", "postgres://localhost:5432/app", ""},
		{"multiple refs", "api=${api.PORT} admin=${api.ADMIN}", "api=8080 admin=8081", ""},
		{"port falls back to PORT", "${api.port}", "8080", ""},
		{"unknown service", "${nope.port}", "", "unresolved reference ${nope.port}"},
		{"unknown key", "${api.WHAT}", "", "unresolved reference ${api.WHAT}"},
		{"hyphenated service name", "${api-main.port}", "", "unresolved reference ${api-main.port}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.in, pm)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandHyphenatedServiceName(t *testing.T) {
	pm := PortMap{"api-main": {"port": 3001, "PORT": 3001}}
	got, err := Expand("http://localhost:${api-main.port}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://localhost:3001" {
		t.Errorf("got %q, want http://localhost:3001", got)
	}
}

func TestExpandSelfReference(t *testing.T) {
	pm := PortMap{"web": {"port": 4000, "PORT": 4000}}
	got, err := Expand("me=${web.port}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "me=4000" {
		t.Errorf("got %q, want me=4000", got)
	}
}

func TestExpandEmptyString(t *testing.T) {
	got, err := Expand("", PortMap{"db": {"PORT": 5432}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpandEmptyPortMap(t *testing.T) {
	_, err := Expand("${db.port}", PortMap{})
	if err == nil {
		t.Fatal("expected error with empty port map, got nil")
	}
}

func TestExpandLiteralsPassThrough(t *testing.T) {
	pm := PortMap{"db": {"PORT": 5432}}
	cases := []string{
		"no dollar here",
		"$PATH is not a ref",
		"${incomplete",
		"${no_dot_here}",
		"$$literal",
		"${.port}",
		"${db.}",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := Expand(in, pm)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", in, err)
			}
			if got != in {
				t.Errorf("literal %q was modified to %q", in, got)
			}
		})
	}
}

func TestExpandAdjacentRefs(t *testing.T) {
	pm := PortMap{
		"a": {"port": 1000, "PORT": 1000},
		"b": {"port": 2000, "PORT": 2000},
	}
	got, err := Expand("${a.port}${b.port}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "10002000" {
		t.Errorf("got %q, want 10002000", got)
	}
}

func TestExpandExactCaseWinsOverFallback(t *testing.T) {
	pm := PortMap{"api": {"port": 1234, "PORT": 9999}}
	got, err := Expand("${api.port}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1234" {
		t.Errorf("got %q, want 1234 (exact-case match should win over PORT fallback)", got)
	}
}

func TestExpandNoFallbackForNonPortKey(t *testing.T) {
	pm := PortMap{"api": {"ADMIN": 9000}}
	_, err := Expand("${api.admin}", pm)
	if err == nil {
		t.Fatal("expected error: fallback should only apply to 'port' key, not arbitrary lowercase")
	}
}

func TestExpandServiceNameUnderscores(t *testing.T) {
	pm := PortMap{"my_db_1": {"port": 5432, "PORT": 5432}}
	got, err := Expand("${my_db_1.port}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "5432" {
		t.Errorf("got %q, want 5432", got)
	}
}

func TestExpandAllEnvVar(t *testing.T) {
	pm := PortMap{"api": {"PORT": 8080}}
	em := EnvMap{
		"api":     {"PORT": "8080", "NAME": "api / main"},
		"web":     {"API_URL": "http://localhost:8080", "NAME": "web"},
		"db-main": {"DB_PORT": "5432"},
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple env ref", "${api.env.NAME}", "api / main"},
		{"embedded", "URL=${web.env.API_URL}", "URL=http://localhost:8080"},
		{"hyphenated svc", "${db-main.env.DB_PORT}", "5432"},
		{"mixed port and env", "${api.PORT} / ${api.env.NAME}", "8080 / api / main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandAll(tt.in, pm, em)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandAllEnvVarUnknownService(t *testing.T) {
	_, err := ExpandAll("${nope.env.FOO}", PortMap{}, EnvMap{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such service") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExpandAllEnvVarUnknownKey(t *testing.T) {
	_, err := ExpandAll("${api.env.MISSING}", PortMap{}, EnvMap{"api": {"PORT": "8080"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExpandRejectsEnvRefWhenEnvMapNil(t *testing.T) {
	// Expand is the port-only wrapper — env refs must error.
	_, err := Expand("${api.env.PORT}", PortMap{"api": {"PORT": 8080}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "env-var references are not allowed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLookupRef(t *testing.T) {
	pm := PortMap{"api": {"PORT": 8080}}
	em := EnvMap{"api": {"PORT": "8080", "NAME": "api"}}
	tests := []struct {
		ref  string
		want string
	}{
		{"api.PORT", "8080"},
		{"api.env.NAME", "api"},
		{"api.env.PORT", "8080"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := LookupRef(tt.ref, pm, em)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLookupRefPreservesDollarBracesInValue(t *testing.T) {
	// A resolved env var may legitimately contain "${...}" text meant for
	// later shell/app expansion. LookupRef must not treat that as an error.
	em := EnvMap{"api": {"TEMPLATE": "${HOME}/bin", "MIXED": "pre ${X} post"}}
	cases := map[string]string{
		"api.env.TEMPLATE": "${HOME}/bin",
		"api.env.MIXED":    "pre ${X} post",
	}
	for ref, want := range cases {
		t.Run(ref, func(t *testing.T) {
			got, err := LookupRef(ref, PortMap{}, em)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestLookupRefInvalid(t *testing.T) {
	cases := []string{"", "not-a-ref", "api", "api.env"}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := LookupRef(in, PortMap{"api": {"PORT": 8080}}, EnvMap{"api": {"PORT": "8080"}}); err == nil {
				t.Errorf("expected error for %q", in)
			}
		})
	}
}

func TestExpandFirstUnresolvedReferenceWins(t *testing.T) {
	_, err := Expand("${a.port} and ${b.port}", PortMap{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "${a.port}") {
		t.Errorf("expected error to mention the first unresolved ref ${a.port}, got %v", err)
	}
}

func TestExpandRefAtStartMiddleEnd(t *testing.T) {
	pm := PortMap{"svc": {"port": 7777, "PORT": 7777}}
	cases := map[string]string{
		"${svc.port} end":   "7777 end",
		"start ${svc.port}": "start 7777",
		"a ${svc.port} b":   "a 7777 b",
		"${svc.port}":       "7777",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := Expand(in, pm)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestExpandMultiPortServiceByEnvKey(t *testing.T) {
	pm := PortMap{
		"infra": {"API_PORT": 4001, "AUTH_PORT": 5001},
	}
	got, err := Expand("api=${infra.API_PORT} auth=${infra.AUTH_PORT}", pm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "api=4001 auth=5001" {
		t.Errorf("got %q, want api=4001 auth=5001", got)
	}
}

func TestExpandMultiPortNoPortKeyErrors(t *testing.T) {
	pm := PortMap{"infra": {"API_PORT": 4001}}
	_, err := Expand("${infra.port}", pm)
	if err == nil {
		t.Fatal("expected error: multi-port service with no 'port' or 'PORT' entry should not resolve ${svc.port}")
	}
}

func TestExpandWithCrossRepoRefs(t *testing.T) {
	resolver := func(repo, svc string, isEnv bool, key string) (string, bool) {
		// Stand-in for the orchestrator: returns canned data for repo "backend"
		// and rejects everything else.
		if repo != "backend" {
			return "", false
		}
		if svc == "api" && !isEnv && key == "port" {
			return "9999", true
		}
		if svc == "api" && isEnv && key == "AUTH_TOKEN" {
			return "secret-xyz", true
		}
		return "", false
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"@repo.svc.port", "${@backend.api.port}", "9999"},
		{"@repo.svc.env.VAR", "token=${@backend.api.env.AUTH_TOKEN}", "token=secret-xyz"},
		{"missing peer with default", "${@backend.unknown.port:-3001}", "3001"},
		{"missing peer empty default", "${@unknown.x.port:-}", ""},
		{"mixed local and remote", "L=${api.port} R=${@backend.api.port}", "L=8080 R=9999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandWith(tt.in, PortMap{"api": {"PORT": 8080}}, EnvMap{}, resolver)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandWithCrossRepoUnresolvedErrors(t *testing.T) {
	resolver := func(repo, svc string, isEnv bool, key string) (string, bool) {
		return "", false
	}
	_, err := ExpandWith("${@backend.api.port}", PortMap{}, EnvMap{}, resolver)
	if err == nil {
		t.Fatal("expected error for unresolved cross-repo ref without default")
	}
	if !strings.Contains(err.Error(), "peer not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExpandWithCrossRepoNoResolverErrors(t *testing.T) {
	_, err := ExpandWith("${@backend.api.port}", PortMap{}, EnvMap{}, nil)
	if err == nil {
		t.Fatal("expected error: nil resolver should reject @-refs without default")
	}
	if !strings.Contains(err.Error(), "cross-repo") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExpandLocalRefDefault(t *testing.T) {
	got, err := Expand("${missing.port:-1234}", PortMap{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1234" {
		t.Errorf("got %q, want 1234", got)
	}
}

func TestExpandAllLocalEnvDefault(t *testing.T) {
	got, err := ExpandAll("${api.env.MISSING:-fallback}", PortMap{}, EnvMap{"api": {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestLookupRefWithCrossRepo(t *testing.T) {
	resolver := func(repo, svc string, isEnv bool, key string) (string, bool) {
		if repo == "backend" && svc == "api" && !isEnv && key == "port" {
			return "9999", true
		}
		return "", false
	}
	got, err := LookupRefWith("@backend.api.port", "", false, PortMap{}, EnvMap{}, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "9999" {
		t.Errorf("got %q, want 9999", got)
	}
}

func TestLookupRefWithFallback(t *testing.T) {
	resolver := func(repo, svc string, isEnv bool, key string) (string, bool) {
		return "", false
	}
	got, err := LookupRefWith("@backend.api.port", "3001", true, PortMap{}, EnvMap{}, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3001" {
		t.Errorf("got %q, want 3001 (fallback)", got)
	}
}

func TestLookupRefRejectsInvalid(t *testing.T) {
	cases := []string{"@.svc.port", "@repo.svc", "@repo..port", "@repo.svc."}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := LookupRefWith(in, "", false, PortMap{}, EnvMap{}, nil); err == nil {
				t.Errorf("expected error for %q", in)
			}
		})
	}
}

func TestHasCrossRepoRef(t *testing.T) {
	cases := map[string]bool{
		"plain":                     false,
		"${api.port}":               false,
		"${api.env.VAR}":            false,
		"${@backend.api.port}":      true,
		"L=${api.port} R=${@b.x.y}": true,
		"${@b.x.y:-default}":        true,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := HasCrossRepoRef(in); got != want {
				t.Errorf("HasCrossRepoRef(%q) = %v, want %v", in, got, want)
			}
		})
	}
}

func TestIsCrossRepoBareRef(t *testing.T) {
	cases := map[string]bool{
		"api.port":             false,
		"api.env.VAR":          false,
		"@backend.api.port":    true,
		"@backend.api.env.VAR": true,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := IsCrossRepoBareRef(in); got != want {
				t.Errorf("IsCrossRepoBareRef(%q) = %v, want %v", in, got, want)
			}
		})
	}
}
