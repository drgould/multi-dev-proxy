package registry

import (
	"sync"
	"testing"
	"time"
)

func TestRegister(t *testing.T) {
	tests := []struct {
		name    string
		entry   *ServerEntry
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid entry",
			entry: &ServerEntry{
				Name: "repo/main",
				Repo: "repo",
				Port: 3000,
				PID:  1234,
			},
			wantErr: false,
		},
		{
			name: "empty name",
			entry: &ServerEntry{
				Name: "",
				Repo: "repo",
				Port: 3000,
				PID:  1234,
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "zero port",
			entry: &ServerEntry{
				Name: "repo/main",
				Repo: "repo",
				Port: 0,
				PID:  1234,
			},
			wantErr: true,
			errMsg:  "port must be positive",
		},
		{
			name: "negative port",
			entry: &ServerEntry{
				Name: "repo/main",
				Repo: "repo",
				Port: -1,
				PID:  1234,
			},
			wantErr: true,
			errMsg:  "port must be positive",
		},
		{
			name: "zero PID allowed",
			entry: &ServerEntry{
				Name: "repo/main",
				Repo: "repo",
				Port: 3000,
				PID:  0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			err := r.Register(tt.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err.Error() != tt.errMsg {
				t.Errorf("Register() error = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}

	t.Run("overwrite existing", func(t *testing.T) {
		r := New()
		entry1 := &ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234}
		entry2 := &ServerEntry{Name: "repo/main", Repo: "repo", Port: 3001, PID: 5678}

		if err := r.Register(entry1); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		if err := r.Register(entry2); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		got := r.Get("repo/main")
		if got.Port != 3001 || got.PID != 5678 {
			t.Errorf("Register() overwrite failed: got Port=%d PID=%d, want Port=3001 PID=5678", got.Port, got.PID)
		}
	})

	t.Run("RegisteredAt auto-set", func(t *testing.T) {
		r := New()
		entry := &ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234}
		before := time.Now()
		if err := r.Register(entry); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		after := time.Now()

		if entry.RegisteredAt.Before(before) || entry.RegisteredAt.After(after) {
			t.Errorf("RegisteredAt not set correctly: got %v, want between %v and %v", entry.RegisteredAt, before, after)
		}
	})
}

func TestDeregister(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*Registry)
		toRemove string
		want     bool
	}{
		{
			name: "existing entry",
			setup: func(r *Registry) {
				r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234})
			},
			toRemove: "repo/main",
			want:     true,
		},
		{
			name:     "non-existent entry",
			setup:    func(r *Registry) {},
			toRemove: "repo/main",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			tt.setup(r)
			got := r.Deregister(tt.toRemove)
			if got != tt.want {
				t.Errorf("Deregister() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("entry removed after deregister", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234})
		r.Deregister("repo/main")
		if got := r.Get("repo/main"); got != nil {
			t.Errorf("Get() after Deregister() = %v, want nil", got)
		}
	})
}

func TestGet(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Registry)
		getName string
		want    *ServerEntry
	}{
		{
			name: "existing entry",
			setup: func(r *Registry) {
				r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234})
			},
			getName: "repo/main",
			want:    &ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000, PID: 1234},
		},
		{
			name:    "missing entry",
			setup:   func(r *Registry) {},
			getName: "repo/main",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			tt.setup(r)
			got := r.Get(tt.getName)
			if tt.want == nil {
				if got != nil {
					t.Errorf("Get() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Errorf("Get() = nil, want %v", tt.want)
				} else if got.Name != tt.want.Name || got.Port != tt.want.Port || got.PID != tt.want.PID {
					t.Errorf("Get() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Registry)
		want  int
	}{
		{
			name:  "empty list",
			setup: func(r *Registry) {},
			want:  0,
		},
		{
			name: "multiple entries",
			setup: func(r *Registry) {
				r.Register(&ServerEntry{Name: "repo1/main", Repo: "repo1", Port: 3000, PID: 1234})
				r.Register(&ServerEntry{Name: "repo2/dev", Repo: "repo2", Port: 3001, PID: 5678})
				r.Register(&ServerEntry{Name: "repo3/feature", Repo: "repo3", Port: 3002, PID: 9012})
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			tt.setup(r)
			got := r.List()
			if len(got) != tt.want {
				t.Errorf("List() length = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestListGroupedByRepo(t *testing.T) {
	t.Run("servers from 2 repos grouped correctly", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo1/main", Repo: "repo1", Port: 3000, PID: 1234})
		r.Register(&ServerEntry{Name: "repo1/dev", Repo: "repo1", Port: 3001, PID: 5678})
		r.Register(&ServerEntry{Name: "repo2/main", Repo: "repo2", Port: 3002, PID: 9012})

		got := r.ListGroupedByRepo()

		if len(got) != 2 {
			t.Errorf("ListGroupedByRepo() groups = %d, want 2", len(got))
		}
		if len(got["repo1"]) != 2 {
			t.Errorf("ListGroupedByRepo() repo1 count = %d, want 2", len(got["repo1"]))
		}
		if len(got["repo2"]) != 1 {
			t.Errorf("ListGroupedByRepo() repo2 count = %d, want 1", len(got["repo2"]))
		}
	})

	t.Run("empty registry", func(t *testing.T) {
		r := New()
		got := r.ListGroupedByRepo()
		if len(got) != 0 {
			t.Errorf("ListGroupedByRepo() on empty registry = %d groups, want 0", len(got))
		}
	})
}

func TestCount(t *testing.T) {
	t.Run("increments on register", func(t *testing.T) {
		r := New()
		if r.Count() != 0 {
			t.Errorf("Count() initial = %d, want 0", r.Count())
		}
		r.Register(&ServerEntry{Name: "repo1/main", Repo: "repo1", Port: 3000, PID: 1234})
		if r.Count() != 1 {
			t.Errorf("Count() after 1 register = %d, want 1", r.Count())
		}
		r.Register(&ServerEntry{Name: "repo2/main", Repo: "repo2", Port: 3001, PID: 5678})
		if r.Count() != 2 {
			t.Errorf("Count() after 2 registers = %d, want 2", r.Count())
		}
	})

	t.Run("decrements on deregister", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo1/main", Repo: "repo1", Port: 3000, PID: 1234})
		r.Register(&ServerEntry{Name: "repo2/main", Repo: "repo2", Port: 3001, PID: 5678})
		if r.Count() != 2 {
			t.Errorf("Count() after 2 registers = %d, want 2", r.Count())
		}
		r.Deregister("repo1/main")
		if r.Count() != 1 {
			t.Errorf("Count() after 1 deregister = %d, want 1", r.Count())
		}
		r.Deregister("repo2/main")
		if r.Count() != 0 {
			t.Errorf("Count() after 2 deregisters = %d, want 0", r.Count())
		}
	})
}

func TestDefaultUpstream(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000})
		if err := r.SetDefault("repo/main"); err != nil {
			t.Fatalf("SetDefault() error = %v", err)
		}
		if got := r.GetDefault(); got != "repo/main" {
			t.Errorf("GetDefault() = %q, want %q", got, "repo/main")
		}
	})

	t.Run("set nonexistent server", func(t *testing.T) {
		r := New()
		if err := r.SetDefault("repo/main"); err == nil {
			t.Error("SetDefault() expected error for nonexistent server")
		}
	})

	t.Run("clear", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000})
		r.SetDefault("repo/main")
		r.ClearDefault()
		if got := r.GetDefault(); got != "" {
			t.Errorf("GetDefault() after ClearDefault() = %q, want empty", got)
		}
	})

	t.Run("deregister clears default", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000})
		r.Register(&ServerEntry{Name: "repo/dev", Repo: "repo", Port: 3001})
		r.SetDefault("repo/main")
		r.Deregister("repo/main")
		if got := r.GetDefault(); got != "" {
			t.Errorf("GetDefault() after deregistering default = %q, want empty", got)
		}
	})

	t.Run("deregister non-default preserves default", func(t *testing.T) {
		r := New()
		r.Register(&ServerEntry{Name: "repo/main", Repo: "repo", Port: 3000})
		r.Register(&ServerEntry{Name: "repo/dev", Repo: "repo", Port: 3001})
		r.SetDefault("repo/main")
		r.Deregister("repo/dev")
		if got := r.GetDefault(); got != "repo/main" {
			t.Errorf("GetDefault() = %q, want %q", got, "repo/main")
		}
	})

	t.Run("empty default by default", func(t *testing.T) {
		r := New()
		if got := r.GetDefault(); got != "" {
			t.Errorf("GetDefault() on new registry = %q, want empty", got)
		}
	})
}

func TestGroupField(t *testing.T) {
	r := New()
	entry := &ServerEntry{Name: "repo/main", Repo: "repo", Group: "main", Port: 3000}
	if err := r.Register(entry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	got := r.Get("repo/main")
	if got.Group != "main" {
		t.Errorf("Group = %q, want %q", got.Group, "main")
	}
}

func TestConcurrent(t *testing.T) {
	t.Run("50 goroutines concurrently registering different names", func(t *testing.T) {
		r := New()
		var wg sync.WaitGroup
		numGoroutines := 50

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				name := "repo/branch-" + string(rune(idx))
				entry := &ServerEntry{
					Name: name,
					Repo: "repo",
					Port: 3000 + idx,
					PID:  1000 + idx,
				}
				if err := r.Register(entry); err != nil {
					t.Errorf("Register() error = %v", err)
				}
			}(i)
		}

		wg.Wait()

		if r.Count() != numGoroutines {
			t.Errorf("Count() after concurrent registers = %d, want %d", r.Count(), numGoroutines)
		}
	})

	t.Run("concurrent reads and writes", func(t *testing.T) {
		r := New()
		var wg sync.WaitGroup
		numOps := 100

		for i := 0; i < numOps; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					r.Register(&ServerEntry{
						Name: "repo/branch-" + string(rune(idx)),
						Repo: "repo",
						Port: 3000 + idx,
						PID:  1000 + idx,
					})
				} else {
					r.Get("repo/branch-" + string(rune(idx-1)))
					r.List()
					r.Count()
				}
			}(i)
		}

		wg.Wait()
	})
}
