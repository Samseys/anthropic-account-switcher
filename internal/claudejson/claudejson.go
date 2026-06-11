// Package claudejson implements a string-aware locator for top-level values in
// ~/.claude.json. It is deliberately NOT a full JSON parser.
//
// ~/.claude.json can contain duplicate object keys (e.g. project paths that
// differ only in drive-letter case). A normal parse-and-rewrite would either
// fail or silently drop one of the duplicates. So we splice only the
// oauthAccount/userID values by byte offset, leaving the rest of the document
// byte-for-byte intact.
//
// This is the single source of truth for the splice algorithm. The previous
// PowerShell and embedded-Python implementations were line-for-line ports of
// each other; keeping it in one place removes that drift hazard.
package claudejson

import (
	"regexp"
	"strings"
)

// Span is a half-open byte range [Start, End) into a document.
type Span struct {
	Start, End int
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// valueEnd returns the index just past the JSON value that starts at index i.
// Indices are byte offsets; this is safe for UTF-8 because every byte we test
// against is ASCII and multi-byte continuation bytes never match a delimiter.
func valueEnd(t string, i int) int {
	n := len(t)
	if i >= n {
		return i
	}
	switch t[i] {
	case '"':
		i++
		for i < n {
			switch t[i] {
			case '\\':
				i += 2
				continue
			case '"':
				return i + 1
			}
			i++
		}
		return i
	case '{', '[':
		depth := 0
		inStr := false
		for i < n {
			c := t[i]
			if inStr {
				if c == '\\' {
					i += 2
					continue
				}
				if c == '"' {
					inStr = false
				}
				i++
				continue
			}
			switch c {
			case '"':
				inStr = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return i
	}
	// primitive: number / true / false / null
	for i < n && t[i] != ',' && t[i] != '}' && t[i] != ']' {
		i++
	}
	return i
}

// TopLevelSpan locates the value of the property key at object depth 1.
// It returns the span and true, or false if the key is absent. Duplicate or
// nested keys at any other depth are ignored.
func TopLevelSpan(t, key string) (Span, bool) {
	i, n, depth := 0, len(t), 0
	for i < n {
		ch := t[i]
		if ch == '"' {
			i++
			var sb strings.Builder
			for i < n {
				c := t[i]
				if c == '\\' {
					if i+1 < n {
						sb.WriteByte(t[i+1])
					}
					i += 2
					continue
				}
				if c == '"' {
					break
				}
				sb.WriteByte(c)
				i++
			}
			i++ // past the closing quote
			if depth == 1 {
				j := i
				for j < n && isSpace(t[j]) {
					j++
				}
				if j < n && t[j] == ':' && sb.String() == key {
					j++
					for j < n && isSpace(t[j]) {
						j++
					}
					return Span{Start: j, End: valueEnd(t, j)}, true
				}
			}
			continue
		}
		switch ch {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		i++
	}
	return Span{}, false
}

// TopLevelValue returns the raw text of the top-level key's value (including
// surrounding quotes/braces), or "" and false if absent.
func TopLevelValue(t, key string) (string, bool) {
	s, ok := TopLevelSpan(t, key)
	if !ok {
		return "", false
	}
	return t[s.Start:s.End], true
}

// SetTopLevelValue replaces the value of the top-level key with newText and
// returns the new document. If the key is absent the document is unchanged.
func SetTopLevelValue(t, key, newText string) string {
	s, ok := TopLevelSpan(t, key)
	if !ok {
		return t
	}
	return t[:s.Start] + newText + t[s.End:]
}

// Field extracts a string field from an object's raw text (e.g. emailAddress
// out of the oauthAccount value). Returns "" if not found.
func Field(objText, field string) string {
	if objText == "" {
		return ""
	}
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(field) + `"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	if m := re.FindStringSubmatch(objText); m != nil {
		return m[1]
	}
	return ""
}
