// Package envexport writes .env files for the mdp orchestrator.
//
// Two flavors exist:
//   - Per-service: the exact env passed to a service's process.
//   - Global: an explicit user-defined map, with values resolved via
//     envexpand so they can reference other services.
package envexport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

const header = "# Managed by mdp — mdp-managed keys are updated on start.\n"

// WritePerService writes a .env file containing the given KEY=VALUE entries.
// The env slice is in "KEY=VALUE" form (same shape as os/exec's Cmd.Env).
// New files get sorted keys for deterministic diffs; existing files are
// merged — managed keys updated in place, new keys appended sorted, all
// other content preserved.
func WritePerService(path string, env []string) error {
	pairs := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := splitKV(kv)
		if !ok {
			continue
		}
		pairs[k] = v
	}
	return writeFile(path, pairs)
}

// WriteGlobal resolves each entry in globalEnv against pm/em and writes the
// result to path. Cross-repo @-references are not supported; use
// WriteGlobalWith to enable them.
func WriteGlobal(path string, globalEnv map[string]config.EnvValue, pm envexpand.PortMap, em envexpand.EnvMap) error {
	return WriteGlobalWith(path, globalEnv, pm, em, nil)
}

// WriteGlobalWith is WriteGlobal with a Resolver for cross-repo @-references.
// Unresolved cross-repo refs without a default are omitted from the output
// (graceful degradation); unresolved local refs without a default error.
func WriteGlobalWith(path string, globalEnv map[string]config.EnvValue, pm envexpand.PortMap, em envexpand.EnvMap, resolver envexpand.Resolver) error {
	pairs := make(map[string]string, len(globalEnv))
	for k, entry := range globalEnv {
		if entry.Ref != "" {
			val, err := envexpand.LookupRefWith(entry.Ref, entry.DefaultValue(), entry.HasDefault(), pm, em, resolver)
			if err != nil {
				if entry.HasDefault() {
					pairs[k] = entry.DefaultValue()
					continue
				}
				if envexpand.IsCrossRepoBareRef(entry.Ref) {
					continue // omit unresolved cross-repo refs without default
				}
				return fmt.Errorf("global env %q: %w", k, err)
			}
			pairs[k] = val
			continue
		}
		val, err := envexpand.ExpandWith(entry.Value, pm, em, resolver)
		if err != nil {
			return fmt.Errorf("global env %q: %w", k, err)
		}
		pairs[k] = val
	}
	return writeFile(path, pairs)
}

// writeFile writes pairs to path. If the file does not exist it is created
// with a header and sorted KEY="VALUE" lines. If it exists, lines assigning
// keys in pairs are updated in place, all other lines are preserved verbatim,
// and remaining new keys are appended at the end (sorted).
func writeFile(path string, pairs map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var b strings.Builder
	written := make(map[string]bool, len(pairs))
	if err == nil {
		mergeLines(&b, string(existing), pairs, written)
	} else {
		b.WriteString(header)
	}

	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		if !written[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeAssignment(&b, k, pairs[k])
		b.WriteString("\n")
	}

	mode := os.FileMode(0o600)
	if info, serr := os.Stat(path); serr == nil {
		mode = info.Mode().Perm() // preserve existing permissions
	}
	// Unique temp file + atomic rename: a crash mid-write cannot truncate
	// user-authored content we can't regenerate, and concurrent writers
	// never share an in-flight temp.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(b.String())
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmpName, mode)
	}
	if werr == nil {
		werr = os.Rename(tmpName, path)
	}
	if werr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, werr)
	}
	return nil
}

// writeAssignment writes KEY="escaped value" (no trailing newline) to b.
func writeAssignment(b *strings.Builder, k, v string) {
	b.WriteString(k)
	b.WriteString("=\"")
	b.WriteString(escape(v))
	b.WriteString("\"")
}

// mergeLines copies content to b, replacing assignments of keys present in
// pairs and recording them in written. An assignment's value may span
// multiple lines when quoted; the whole assignment is replaced with a single
// line, preserving the leading prefix (whitespace, "export ") and trailing
// suffix (e.g. an inline comment).
func mergeLines(b *strings.Builder, content string, pairs map[string]string, written map[string]bool) {
	if bom := "\xef\xbb\xbf"; strings.HasPrefix(content, bom) {
		b.WriteString(bom) // keep the BOM but don't let it block key matching
		content = content[len(bom):]
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	for len(content) > 0 {
		key, prefix, suffix, consumed, ok := parseAssignment(content)
		if !ok {
			nl := strings.IndexByte(content, '\n')
			b.WriteString(content[:nl+1])
			content = content[nl+1:]
			continue
		}
		if v, managed := pairs[key]; managed {
			b.WriteString(prefix)
			writeAssignment(b, key, v)
			b.WriteString(suffix)
			b.WriteString("\n")
			written[key] = true
		} else {
			b.WriteString(content[:consumed])
		}
		content = content[consumed:]
	}
}

// parseAssignment parses an env assignment at the start of content (which is
// newline-terminated). It returns the key, the leading prefix (whitespace and
// optional "export "), the suffix after the value on its final line (e.g. an
// inline comment), and the number of bytes consumed including the trailing
// newline. ok is false if content does not start with an assignment.
func parseAssignment(content string) (key, prefix, suffix string, consumed int, ok bool) {
	i := skipBlank(content, 0)
	if rest := content[i:]; strings.HasPrefix(rest, "export") && skipBlank(content, i+6) > i+6 {
		i = skipBlank(content, i+6)
	}
	prefix = content[:i]
	start := i
	for i < len(content) && isKeyByte(content[i]) {
		i++
	}
	if i == start {
		return "", "", "", 0, false
	}
	key = content[start:i]
	i = skipBlank(content, i)
	if i >= len(content) || content[i] != '=' {
		return "", "", "", 0, false
	}
	end := valueEnd(content, i+1)
	nl := strings.IndexByte(content[end:], '\n')
	if nl < 0 { // unterminated quote ran to EOF
		return key, prefix, "", len(content), true
	}
	return key, prefix, content[end : end+nl], end + nl + 1, true
}

// valueEnd returns the index just past the value that starts at i (right
// after the '='). Quoted values may span lines; unquoted values end at the
// line end or before a whitespace-preceded inline '#' comment.
func valueEnd(content string, i int) int {
	start := i
	i = skipBlank(content, i)
	switch {
	case i < len(content) && content[i] == '"':
		for j := i + 1; j < len(content); j++ {
			switch content[j] {
			case '\\':
				j++
			case '"':
				return j + 1
			}
		}
		return len(content)
	case i < len(content) && content[i] == '\'':
		if j := strings.IndexByte(content[i+1:], '\''); j >= 0 {
			return i + 1 + j + 1
		}
		return len(content)
	default:
		end := i
		for end < len(content) && content[end] != '\n' {
			end++
		}
		for j := i; j < end; j++ {
			if content[j] == '#' && j > start && isBlank(content[j-1]) {
				for j > start && isBlank(content[j-1]) {
					j-- // keep the whitespace before '#' in the suffix
				}
				return j
			}
		}
		return end
	}
}

func skipBlank(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func isKeyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// escape prepares a value for inclusion inside double-quoted dotenv syntax.
func escape(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitKV(kv string) (string, string, bool) {
	i := strings.IndexByte(kv, '=')
	if i < 0 {
		return "", "", false
	}
	return kv[:i], kv[i+1:], true
}
