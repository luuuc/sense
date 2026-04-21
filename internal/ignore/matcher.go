// Package ignore provides gitignore-compatible path matching for Sense's
// file walker. It composes rules from .gitignore, .senseignore, and
// config-level ignore patterns into a single predicate.
package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

var defaultPatterns = []string{
	"vendor/",
	"node_modules/",
	"dist/",
	"build/",
	"*.min.js",
	"*.bundle.js",
	"*.min.css",
}

// DefaultPatterns returns a copy of the built-in ignore patterns.
func DefaultPatterns() []string {
	out := make([]string, len(defaultPatterns))
	copy(out, defaultPatterns)
	return out
}

// Matcher tests whether a path should be excluded from the scan.
type Matcher struct {
	rules []rule
}

type rule struct {
	pattern  string
	negated  bool
	dirOnly  bool
	anchored bool   // pattern contains a slash → only matches from root
	scopeDir string // non-empty for unanchored patterns from nested .gitignore
}

// New creates a Matcher from the given pattern lines (gitignore syntax).
// Each call to Add layers more rules on top.
func New(patterns ...string) *Matcher {
	m := &Matcher{}
	for _, p := range patterns {
		m.addLine(p)
	}
	return m
}

// Add appends rules from pattern lines.
func (m *Matcher) Add(patterns ...string) {
	for _, p := range patterns {
		m.addLine(p)
	}
}

// AddFromFile reads a gitignore-format file and appends its rules.
// prefix is prepended to anchored patterns so rules from nested
// directories match correctly (e.g. prefix="sub/dir" for sub/dir/.gitignore).
// A missing file is not an error — it's simply a no-op.
func (m *Matcher) AddFromFile(path, prefix string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		r, ok := parseLine(line)
		if !ok {
			continue
		}
		if prefix != "" {
			if r.anchored {
				r.pattern = prefix + "/" + r.pattern
			} else {
				// Unanchored patterns from nested dirs only apply under that dir.
				// Scope them by setting a directory prefix; the matcher will
				// restrict matching to paths under this prefix.
				r.scopeDir = prefix
			}
		}
		m.rules = append(m.rules, r)
	}
	return scanner.Err()
}

// Match returns true if the relative path should be ignored.
// isDir should be true when the path refers to a directory.
func (m *Matcher) Match(rel string, isDir bool) bool {
	rel = filepath.ToSlash(rel)
	matched := false
	for _, r := range m.rules {
		target := rel
		if r.scopeDir != "" {
			// Pattern from nested .gitignore — only applies under that dir.
			prefix := r.scopeDir + "/"
			if !strings.HasPrefix(rel, prefix) {
				continue
			}
			target = rel[len(prefix):]
		}
		if r.dirOnly && !isDir {
			if !matchAsParent(r.pattern, target, r.anchored) {
				continue
			}
		} else if !matchPattern(r.pattern, target, r.anchored) {
			continue
		}
		matched = !r.negated
	}
	return matched
}

// matchAsParent returns true when rel is a path *inside* a directory
// that would be matched by pattern. For example, pattern "vendor" matches
// rel "vendor/gems/foo.rb".
func matchAsParent(pattern, rel string, anchored bool) bool {
	prefix := pattern + "/"
	if anchored {
		return len(rel) > len(prefix) && strings.HasPrefix(rel, prefix)
	}
	if len(rel) > len(prefix) && strings.HasPrefix(rel, prefix) {
		return true
	}
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' {
			suffix := rel[i+1:]
			if len(suffix) > len(prefix) && strings.HasPrefix(suffix, prefix) {
				return true
			}
		}
	}
	return false
}

func (m *Matcher) addLine(line string) {
	r, ok := parseLine(line)
	if !ok {
		return
	}
	m.rules = append(m.rules, r)
}

func parseLine(line string) (rule, bool) {
	// Check for escaped trailing space before trimming.
	hasEscapedSpace := strings.HasSuffix(line, "\\ ")
	line = strings.TrimRight(line, " \t")
	if line == "" || line[0] == '#' {
		return rule{}, false
	}
	if hasEscapedSpace {
		line = strings.TrimSuffix(line, "\\") + " "
	}

	r := rule{}
	if strings.HasPrefix(line, "!") {
		r.negated = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if strings.HasPrefix(line, "/") {
		r.anchored = true
		line = line[1:]
	}
	// A pattern with an interior slash is implicitly anchored.
	if !r.anchored && strings.Contains(line, "/") {
		r.anchored = true
	}

	r.pattern = line
	return r, line != ""
}

// matchPattern tests whether rel matches pattern.
// If anchored, the pattern must match from the root. Otherwise it can
// match against any trailing path component(s).
// A pattern that matches a directory also matches all paths under it.
func matchPattern(pattern, rel string, anchored bool) bool {
	if anchored {
		if doGlob(pattern, rel) {
			return true
		}
		prefix := pattern + "/"
		return len(rel) > len(prefix) && strings.HasPrefix(rel, prefix)
	}
	if tryGlobOrPrefix(pattern, rel) {
		return true
	}
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' {
			if tryGlobOrPrefix(pattern, rel[i+1:]) {
				return true
			}
		}
	}
	return false
}

func tryGlobOrPrefix(pattern, candidate string) bool {
	if doGlob(pattern, candidate) {
		return true
	}
	prefix := pattern + "/"
	return len(candidate) > len(prefix) && strings.HasPrefix(candidate, prefix)
}

func doGlob(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			if len(pattern) > 1 && pattern[1] == '*' {
				// ** — match zero or more path components.
				rest := pattern[2:]
				if len(rest) > 0 && rest[0] == '/' {
					rest = rest[1:]
				}
				if rest == "" {
					return true // trailing ** matches everything
				}
				// Try matching rest against every suffix.
				if doGlob(rest, name) {
					return true
				}
				for i := 0; i < len(name); i++ {
					if name[i] == '/' {
						if doGlob(rest, name[i+1:]) {
							return true
						}
					}
				}
				return false
			}
			// Single * — match anything except '/'.
			rest := pattern[1:]
			for i := 0; i <= len(name); i++ {
				if doGlob(rest, name[i:]) {
					return true
				}
				if i < len(name) && name[i] == '/' {
					break
				}
			}
			return false

		case '?':
			if len(name) == 0 || name[0] == '/' {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]

		case '[':
			if len(name) == 0 || name[0] == '/' {
				return false
			}
			// Character class — find closing bracket.
			end := strings.IndexByte(pattern, ']')
			if end < 0 {
				return false
			}
			class := pattern[1:end]
			matched := matchClass(class, name[0])
			if !matched {
				return false
			}
			pattern = pattern[end+1:]
			name = name[1:]

		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		}
	}
	return len(name) == 0
}

func matchClass(class string, ch byte) bool {
	negate := false
	if len(class) > 0 && class[0] == '!' {
		negate = true
		class = class[1:]
	}
	matched := false
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			if ch >= class[i] && ch <= class[i+2] {
				matched = true
			}
			i += 2
		} else if class[i] == ch {
			matched = true
		}
	}
	if negate {
		return !matched
	}
	return matched
}
