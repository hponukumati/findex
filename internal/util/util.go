package util

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var spacey = regexp.MustCompile(`[\s_\-\.]+`)

func ExtLower(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:]
	}
	return ext
}

// Normalize filename for matching: lowercase, replace separators w/ spaces, trim.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = spacey.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	// Collapse weird unicode spacing a bit
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func Tokenize(norm string) []string {
	if norm == "" {
		return nil
	}
	parts := strings.Split(norm, " ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// tiny stopword list (optional)
		switch p {
		case "the", "a", "an", "and", "or", "of", "to":
			continue
		}
		out = append(out, p)
	}
	return out
}

// Trigrams for robust typo-ish matching
func Trigrams(s string) map[string]struct{} {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 3 {
		m := map[string]struct{}{s: {}}
		return m
	}
	m := make(map[string]struct{}, len(s))
	for i := 0; i+3 <= len(s); i++ {
		m[s[i:i+3]] = struct{}{}
	}
	return m
}

func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	// iterate smaller
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
