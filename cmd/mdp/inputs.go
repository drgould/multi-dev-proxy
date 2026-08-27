package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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

// buildInputSteps produces the {name -> value} map for every input that
// resolves without prompting, plus the ordered inputStep list for every input
// that still needs an answer. When interactive, groupsFor populates
// `choices: groups` lists (fetched at most once per repo filter). Non-
// interactive inputs all resolve here: to their Default, or an error if none
// is declared. A `choices: groups` input with no active groups and a
// declared default is skipped silently — so `mdp run -i` only prompts when
// there is something to select; a failed groups fetch (nil) instead degrades
// to a free-text step, since "couldn't list groups" must not silently pick
// the default.
func buildInputSteps(cfg *config.Config, interactive bool, groupsFor func(repo string) []string) (map[string]string, []inputStep, error) {
	values := make(map[string]string, len(cfg.Inputs))
	groupsByRepo := make(map[string][]string) // cache, keyed by repo filter ("" = all)
	groupsFetched := make(map[string]bool)    // separate from the cache: a nil (failed) fetch is cached too
	var steps []inputStep
	for _, spec := range cfg.Inputs {
		if !interactive {
			if !spec.HasDefault {
				return nil, nil, fmt.Errorf("input %q has no default; rerun with -i to provide a value", spec.Name)
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
			// falls through to a free-text step instead of silently defaulting.
			if groups != nil && len(groups) == 0 && spec.HasDefault {
				values[spec.Name] = spec.Default
				continue
			}
		}
		step := inputStep{spec: spec}
		if spec.Choices == "groups" && len(groups) > 0 {
			// "@{current}" is appended as a pickable entry so the
			// fallback-to-own-group sentinel is discoverable; it resolves at
			// lookup time (effectiveGroup).
			step.choices = append(append([]string(nil), groups...), currentGroupSentinel)
		}
		steps = append(steps, step)
	}
	return values, steps, nil
}

// resolveInputs resolves every declared input, prompting for whatever
// buildInputSteps didn't resolve outright via a single Bubble Tea wizard (see
// inputwizard.go). currentGroup is the caller's own group, shown on the
// "@{current}" pick-list entry. isTTY reports whether both stdin and stderr
// are real terminals — required whenever there's at least one step to
// prompt, since the wizard reads keys from stdin but renders to stderr, and
// either being redirected breaks it (injectable so tests don't depend on the
// process's actual streams).
func resolveInputs(ctx context.Context, cfg *config.Config, interactive bool, currentGroup string, groupsFor func(repo string) []string, isTTY func() bool) (map[string]string, error) {
	if len(cfg.Inputs) == 0 {
		return nil, nil
	}
	values, steps, err := buildInputSteps(cfg, interactive, groupsFor)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return values, nil
	}

	if !isTTY() {
		return nil, fmt.Errorf("mdp run -i requires an interactive terminal (stdin or stderr is not a TTY)")
	}
	// A SIGTERM that lands during buildInputSteps/fetchActiveGroups (both
	// ctx-blind) must not still start the wizard — tea.Program.Run puts the
	// terminal in raw mode before it would ever observe an already-cancelled
	// ctx.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	answers, err := runInputWizard(ctx, steps, currentGroup)
	if err != nil {
		return nil, err
	}
	for name, val := range answers {
		values[name] = val
	}
	return values, nil
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
