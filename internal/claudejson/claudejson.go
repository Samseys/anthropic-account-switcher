// Package claudejson is a string-aware, byte-offset splicer for top-level values
// in ~/.claude.json. It is deliberately NOT a full JSON parser: that file can
// contain duplicate object keys, so we splice only oauthAccount/userID by byte
// offset, leaving the rest of the document byte-for-byte intact.
package claudejson

import (
	"regexp"
	"strings"
	"sync"
)

// Span is a half-open byte range [Start, End) into a document.
type Span struct {
	Start, End int
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// valueEnd returns the index just past the JSON value starting at i.
// Safe for UTF-8 because all tested bytes are ASCII.
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

// TopLevelSpan locates the value of key at object depth 1.
// Keys at any other depth are ignored.
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

// TopLevelValue returns the raw text of the top-level key's value, or "" and false if absent.
func TopLevelValue(t, key string) (string, bool) {
	s, ok := TopLevelSpan(t, key)
	if !ok {
		return "", false
	}
	return t[s.Start:s.End], true
}

// SetTopLevelValue replaces the top-level key's value; returns t unchanged if key is absent.
func SetTopLevelValue(t, key, newText string) string {
	s, ok := TopLevelSpan(t, key)
	if !ok {
		return t
	}
	return t[:s.Start] + newText + t[s.End:]
}

var fieldRes sync.Map // field name -> *regexp.Regexp; compiled once per static field name

// Field extracts a string field from an object's raw JSON text; returns "" if not found.
func Field(objText, field string) string {
	if objText == "" {
		return ""
	}
	re, ok := fieldRes.Load(field)
	if !ok {
		re, _ = fieldRes.LoadOrStore(field,
			regexp.MustCompile(`"`+regexp.QuoteMeta(field)+`"\s*:\s*"((?:[^"\\]|\\.)*)"`))
	}
	if m := re.(*regexp.Regexp).FindStringSubmatch(objText); m != nil {
		return m[1]
	}
	return ""
}
