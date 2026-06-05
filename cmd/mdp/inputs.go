package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

// fetchActiveGroups returns the sorted list of groups currently registered with
// the orchestrator, used to populate `choices: groups` pick-lists. Returns nil
// on any error — prompting then degrades to free-text entry.
func fetchActiveGroups(client *http.Client, controlURL string) []string {
	resp, err := client.Get(controlURL + "/__mdp/groups")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var groups map[string][]string
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		return nil
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}

// resolveInputs produces the {name -> value} map for the config's declared
// inputs. When interactive, each input is prompted for (groupsFor populates
// `choices: groups` lists, fetched at most once) reading from in; otherwise
// every input resolves to its Default, and an input with no default is an
// error. in/out are injectable for testing.
func resolveInputs(cfg *config.Config, interactive bool, groupsFor func() []string, in io.Reader, out io.Writer) (map[string]string, error) {
	if len(cfg.Inputs) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(cfg.Inputs))
	reader := bufio.NewReader(in)
	var groups []string
	var groupsLoaded bool
	for _, spec := range cfg.Inputs {
		if !interactive {
			if !spec.HasDefault {
				return nil, fmt.Errorf("input %q has no default; rerun with -i to provide a value", spec.Name)
			}
			values[spec.Name] = spec.Default
			continue
		}
		if spec.Choices == "groups" && !groupsLoaded {
			groups = groupsFor()
			groupsLoaded = true
		}
		val, err := promptInput(spec, groups, reader, out)
		if err != nil {
			return nil, err
		}
		values[spec.Name] = val
	}
	return values, nil
}

// promptInput asks for one input on out, reading the answer from r, and returns
// the resolved value. For `choices: groups` it prints a numbered list of active
// groups (marking the default) and accepts a list number or a typed value; an
// answer matching a group name is taken literally (so a numerically-named group
// stays selectable), and an out-of-range number is taken as a literal value (so
// a not-yet-running branch named "3" can still be entered). An empty answer
// uses the default when one is declared, else re-prompts. EOF (Ctrl-D / end of
// input) aborts. The resolved value — typed or picked — is rejected if it
// contains ${...}, so inputs stay plain literals (no smuggled port/peer refs).
func promptInput(spec config.InputSpec, groups []string, r *bufio.Reader, out io.Writer) (string, error) {
	label := spec.Prompt
	if label == "" {
		label = spec.Name
	}
	pickList := spec.Choices == "groups" && len(groups) > 0
	if pickList {
		fmt.Fprintf(out, "%s\n", label)
		for i, g := range groups {
			marker := ""
			if g == spec.Default {
				marker = " (default)"
			}
			fmt.Fprintf(out, "  %d) %s%s\n", i+1, g, marker)
		}
	}
	for {
		switch {
		case pickList && spec.HasDefault:
			fmt.Fprintf(out, "Select a number, type a branch, or press enter for default [%s]: ", spec.Default)
		case pickList:
			fmt.Fprintf(out, "Select a number or type a branch: ")
		case spec.HasDefault:
			fmt.Fprintf(out, "%s [%s]: ", label, spec.Default)
		default:
			fmt.Fprintf(out, "%s: ", label)
		}

		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read input %q: %w", spec.Name, err)
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			if err == io.EOF {
				return "", fmt.Errorf("input %q cancelled (end of input)", spec.Name)
			}
			if spec.HasDefault {
				return spec.Default, nil
			}
			fmt.Fprintln(out, "  a value is required")
			continue
		}

		value := answer
		// In a pick-list, a bare number selects by 1-based index. An exact
		// group-name match wins first (so a group literally named "456" stays
		// selectable), and an out-of-range number is taken as a literal value
		// (so a not-yet-running branch named "3" can still be entered).
		if pickList && !slices.Contains(groups, answer) {
			if n, convErr := strconv.Atoi(answer); convErr == nil && n >= 1 && n <= len(groups) {
				value = groups[n-1]
			}
		}
		// Guard the resolved value — covers both the typed and pick-list paths.
		if strings.Contains(value, "${") {
			return "", fmt.Errorf("input %q: value must be a plain literal (it cannot contain ${...} references)", spec.Name)
		}
		return value, nil
	}
}

// applyInputs rewrites every ${inputs.X} reference in the loaded config to its
// resolved value, in place, so the downstream pipeline never sees an input
// reference. It drives config.VisitInputRefFields — the single source of truth
// for which fields support inputs — so the substituted set always matches what
// validation checked.
func applyInputs(cfg *config.Config, inputs map[string]string) error {
	var firstErr error
	cfg.VisitInputRefFields(func(where, value string) (string, bool) {
		if firstErr != nil {
			return value, false
		}
		out, err := envexpand.SubstituteInputs(value, inputs)
		if err != nil {
			firstErr = fmt.Errorf("%s: %w", where, err)
			return value, false
		}
		// A `ref:` that substitutes to empty would be silently downgraded to a
		// scalar empty value (KEY=) by env building — reject it instead.
		if out == "" && value != "" && strings.HasSuffix(where, " ref") {
			firstErr = fmt.Errorf("%s resolved to an empty ref", where)
			return value, false
		}
		return out, true
	})
	return firstErr
}

// checkLinkGroups rejects any link whose group resolved to empty — a declared
// link pointing at nothing would silently fall back to the caller's own group.
// Run after merging config links with CLI --link overrides, so an override can
// rescue a config link that resolved empty.
func checkLinkGroups(links map[string]string) error {
	repos := make([]string, 0, len(links))
	for repo := range links {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		if strings.TrimSpace(links[repo]) == "" {
			return fmt.Errorf("link %q resolves to an empty group", repo)
		}
	}
	return nil
}
