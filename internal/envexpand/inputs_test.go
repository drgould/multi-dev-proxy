package envexpand

import (
	"reflect"
	"strings"
	"testing"
)

func TestSubstituteInputs(t *testing.T) {
	inputs := map[string]string{"api_branch": "main", "empty": ""}
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"no refs", "hello", "hello", ""},
		{"single", "${inputs.api_branch}", "main", ""},
		{"embedded", "http://host:${inputs.api_branch}/x", "http://host:main/x", ""},
		{"empty value resolves", "${inputs.empty}", "", ""},
		{"fallback used when absent", "${inputs.missing:-fb}", "fb", ""},
		{"value wins over fallback", "${inputs.api_branch:-fb}", "main", ""},
		{"undeclared no fallback", "${inputs.missing}", "", "unresolved input reference ${inputs.missing}"},
		{"leaves other refs untouched", "${db.port}-${inputs.api_branch}", "${db.port}-main", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SubstituteInputs(tt.in, inputs)
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
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvalidInputRefs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"valid", "${inputs.api_branch}", nil},
		{"valid with fallback", "${inputs.x:-main}", nil},
		{"non-input refs ignored", "hello ${db.port}", nil},
		{"bad name", "${inputs.api-branch}", []string{"${inputs.api-branch}"}},
		{"empty name", "${inputs.}", []string{"${inputs.}"}},
		{"dotted name", "${inputs.a.b}", []string{"${inputs.a.b}"}},
		{"nested ref in fallback", "${inputs.host:-${api.port}}", []string{"${inputs.host:-${api.port}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InvalidInputRefs(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("InvalidInputRefs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestScanInputRefs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"no refs here", nil},
		{"${inputs.a}", []string{"a"}},
		{"${inputs.a}-${inputs.b}-${inputs.a}", []string{"a", "b"}}, // distinct, source order
		{"${inputs.a:-x}", nil},                                     // fallback => optional, not required to be declared
		{"${inputs.a}-${inputs.b:-x}", []string{"a"}},               // only the no-fallback ref is required
		{"${db.port}", nil},
	}
	for _, tt := range tests {
		got := ScanInputRefs(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("ScanInputRefs(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
