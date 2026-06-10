package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

// fetchActiveGroups returns the sorted list of groups currently registered with
// the orchestrator, used to populate `choices: groups` pick-lists. A non-empty
// repo restricts the list to groups containing services from that repo. Returns
// nil on any error and a non-nil empty slice when no groups match — resolveInputs
// prompts free-text on error but skips the input (default) when genuinely empty.
func fetchActiveGroups(client *http.Client, controlURL, repo string) []string {
	target := controlURL + "/__mdp/groups"
	if repo != "" {
		target += "?repo=" + url.QueryEscape(repo)
	}
	resp, err := client.Get(target)
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
// `choices: groups` lists, fetched at most once per repo filter) reading from
// in; otherwise every input resolves to its Default, and an input with no
// default is an error. A `choices: groups` input with no active groups and a
// declared default is skipped silently — exactly like non-interactive — so
// `mdp run -i` only prompts when there is something to select; a failed groups
// fetch (nil) instead degrades to a free-text prompt, since "couldn't list
// groups" must not silently pick the default. currentGroup is
// the caller's own group, shown on the "@{current}" pick-list entry. in/out
// are injectable for testing.
func resolveInputs(cfg *config.Config, interactive bool, currentGroup string, groupsFor func(repo string) []string, in io.Reader, out io.Writer) (map[string]string, error) {
	if len(cfg.Inputs) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(cfg.Inputs))
	reader := bufio.NewReader(in)
	groupsByRepo := make(map[string][]string) // cache, keyed by repo filter ("" = all)
	groupsFetched := make(map[string]bool)    // separate from the cache: a nil (failed) fetch is cached too
	for _, spec := range cfg.Inputs {
		if !interactive {
			if !spec.HasDefault {
				return nil, fmt.Errorf("input %q has no default; rerun with -i to provide a value", spec.Name)
			}
			values[spec.Name] = spec.Default
			continue
		}
		var groups []string
		if spec.Choices == "groups" {
			if !groupsFetched[spec.Repo] {
				groupsByRepo[spec.Repo] = groupsFor(spec.Repo)
				groupsFetched[spec.Repo] = true
			}
			groups = groupsByRepo[spec.Repo]
			// Skip only on a confirmed-empty list (non-nil): a failed fetch (nil)
			// falls through to a free-text prompt instead of silently defaulting.
			if groups != nil && len(groups) == 0 && spec.HasDefault {
				values[spec.Name] = spec.Default
				continue
			}
		}
		val, err := promptInput(spec, currentGroup, groups, reader, out)
		if err != nil {
			return nil, err
		}
		values[spec.Name] = val
	}
	return values, nil
}

// promptInput asks for one input on out, reading the answer from r, and returns
// the resolved value. For `choices: groups` it prints a numbered list of active
// groups plus a final "@{current}" entry (marking the default) and accepts a list
// number or a typed value; an answer matching a list entry is taken literally
// (so a numerically-named group stays selectable), and an out-of-range number
// is taken as a literal value (so a not-yet-running branch named "3" can still
// be entered). An empty answer uses the default when one is declared, else
// re-prompts. EOF (Ctrl-D / end of input) aborts. A `choices: groups` spec
// only reaches here with an empty groups list when it has no default or the
// fetch failed (resolveInputs skips otherwise); it degrades to free text. The resolved
// value — typed or picked — is rejected if it contains ${...}, so inputs stay
// plain literals (no smuggled port/peer refs).
func promptInput(spec config.InputSpec, currentGroup string, groups []string, r *bufio.Reader, out io.Writer) (string, error) {
	label := spec.Prompt
	if label == "" {
		label = spec.Name
	}
	pickList := spec.Choices == "groups" && len(groups) > 0
	var choices []string
	if pickList {
		// "@{current}" is appended as a pickable entry so the fallback-to-own-group
		// sentinel is discoverable; it resolves at lookup time (effectiveGroup).
		choices = append(append([]string(nil), groups...), currentGroupSentinel)
		fmt.Fprintf(out, "%s\n", label)
		for i, g := range choices {
			marker := ""
			if g == spec.Default {
				marker = " (default)"
			}
			// Annotated with the workspace's group; a service-level `group:`
			// override resolves @{current} to its own group instead (effectiveGroup).
			if g == currentGroupSentinel {
				marker = fmt.Sprintf(" — this checkout's default group (%s)%s", currentGroup, marker)
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
		// entry-name match wins first (so a group literally named "456" stays
		// selectable), and an out-of-range number is taken as a literal value
		// (so a not-yet-running branch named "3" can still be entered).
		if pickList && !slices.Contains(choices, answer) {
			if n, convErr := strconv.Atoi(answer); convErr == nil && n >= 1 && n <= len(choices) {
				value = choices[n-1]
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
