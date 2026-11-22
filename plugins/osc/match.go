package main

import "strings"

// MatchAddress reports whether an OSC address matches a filter pattern, using
// the OSC 1.0 pattern syntax: ? matches one character, * matches any run of
// characters, [abc] and [a-z] match character classes ([! negates), and
// {foo,bar} matches one of the listed literals. None of the wildcards cross a
// / separator, so /a/* matches the children of /a and nothing deeper. The
// pattern here is the subscriber's filter matched against the concrete
// address a message arrived with.
func MatchAddress(pattern, address string) bool {
	p, a := pattern, address
	for len(p) > 0 {
		switch p[0] {
		case '*':
			// Collapse a run of stars, then try every stop point within the
			// current path segment.
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return !strings.Contains(a, "/")
			}
			for i := 0; ; i++ {
				if MatchAddress(p, a[i:]) {
					return true
				}
				if i >= len(a) || a[i] == '/' {
					return false
				}
			}
		case '?':
			if len(a) == 0 || a[0] == '/' {
				return false
			}
			p, a = p[1:], a[1:]
		case '[':
			rest, ok := matchClass(p, a)
			if !ok {
				return false
			}
			p, a = rest, a[1:]
		case '{':
			end := strings.IndexByte(p, '}')
			if end < 0 {
				// An unterminated alternation is matched as the literal it is.
				if len(a) == 0 || a[0] != p[0] {
					return false
				}
				p, a = p[1:], a[1:]
				continue
			}
			for _, alt := range strings.Split(p[1:end], ",") {
				if strings.HasPrefix(a, alt) && MatchAddress(p[end+1:], a[len(alt):]) {
					return true
				}
			}
			return false
		default:
			if len(a) == 0 || a[0] != p[0] {
				return false
			}
			p, a = p[1:], a[1:]
		}
	}
	return len(a) == 0
}

// matchClass matches one address character against the [class] at the start
// of the pattern, returning the pattern after the class. An unterminated
// class is matched as a literal [.
func matchClass(p, a string) (rest string, ok bool) {
	end := strings.IndexByte(p, ']')
	if end < 0 {
		if len(a) == 0 || a[0] != '[' {
			return "", false
		}
		return p[1:], true
	}
	if len(a) == 0 || a[0] == '/' {
		return "", false
	}
	c := a[0]
	class := p[1:end]
	negate := strings.HasPrefix(class, "!")
	if negate {
		class = class[1:]
	}
	matched := false
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			if class[i] <= c && c <= class[i+2] {
				matched = true
			}
			i += 2
			continue
		}
		if class[i] == c {
			matched = true
		}
	}
	if matched == negate {
		return "", false
	}
	return p[end+1:], true
}
