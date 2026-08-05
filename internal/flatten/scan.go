// Package flatten implements the @path import flattening rules that Claude Code
// applies when it reads a CLAUDE.md/AGENTS.md file: an @-prefixed token that
// resolves to a real file is replaced by that file's contents, recursively.
package flatten

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// fenceState tracks an open Markdown fenced code block across lines.
//
// CommonMark rules we reproduce: a fence opens with a run of 3+ backticks or
// tildes (leading whitespace allowed) and is closed only by a run of the same
// character that is at least as long. That means a longer outer fence is not
// closed by a shorter inner one, and a tilde line inside a backtick fence is
// just content.
//
// We deliberately accept looser delimiter lines than CommonMark proper:
// unlimited leading indentation (CommonMark caps it at 3 spaces) and closer
// lines with trailing text after the run of fence characters. This matches
// the JS (`line.trimStart()` plus a bare `^(```+|~~~+)` prefix match with no
// end anchor and no indentation limit), which is the behavior we're porting.
type fenceState struct {
	open bool
	char byte // '`' or '~'
	len  int  // opening run length
}

// jsIsSpace reports whether r is in the set matched by JavaScript's regex
// `\s` (Unicode-aware mode), which JS `collectAtTokens`'s `/@(\S+)/g` and
// `findImports`'s `line.trimStart()` both rely on.
//
// The JS `\s` set is: U+0009-U+000D, U+0020, U+00A0, U+1680, U+2000-U+200A,
// U+2028, U+2029, U+202F, U+205F, U+3000, U+FEFF.
//
// This is close to, but not identical to, Go's unicode.IsSpace:
//   - unicode.IsSpace includes U+0085 (NEL), which JS \s does NOT match.
//   - unicode.IsSpace excludes U+FEFF (BOM/ZWNBSP), which JS \s DOES match.
//
// Those are the only two deltas across the entire Unicode range (verified by
// exhaustive scan against JS's own /\s/u.test behavior), so this function is
// unicode.IsSpace with those two cases special-cased.
func jsIsSpace(r rune) bool {
	if r == 0x0085 {
		return false
	}
	if r == 0xFEFF {
		return true
	}
	return unicode.IsSpace(r)
}

// fenceDelim returns the fence character and run length if the line is a fence
// delimiter, or ok=false when it is not. Leading whitespace is stripped using
// the same JS-\s definition as JS's line.trimStart(), so e.g. a form-feed
// before the fence characters still counts as indentation.
func fenceDelim(line string) (char byte, runLen int, ok bool) {
	trimmed := trimLeftJSSpace(line)
	if trimmed == "" {
		return 0, 0, false
	}
	c := trimmed[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return c, n, true
}

// trimLeftJSSpace strips leading runes matching jsIsSpace, mirroring JS
// String.prototype.trimStart() (which strips the same Unicode \s set).
func trimLeftJSSpace(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if !jsIsSpace(r) {
			break
		}
		s = s[size:]
	}
	return s
}

// step feeds a line to the state machine. isDelim reports whether the line is a
// fence delimiter (such lines never contain imports and are copied verbatim);
// inFence reports whether the line's content sits inside a fenced block.
func (f *fenceState) step(line string) (isDelim, inFence bool) {
	if c, n, ok := fenceDelim(line); ok {
		switch {
		case !f.open:
			f.open, f.char, f.len = true, c, n
		case c == f.char && n >= f.len:
			f.open, f.char, f.len = false, 0, 0
		}
		return true, f.open
	}
	return false, f.open
}

// FindImports returns the @path candidates in text, in document order and
// including duplicates, skipping inline code spans and fenced code blocks.
//
// A candidate starts at '@' and runs to the next whitespace character (as
// defined by jsIsSpace); the '@' may appear anywhere in a line. Whether a
// candidate is a real import is decided later by resolving it against the
// filesystem, which is what keeps email addresses and @scope/package mentions
// from being treated as imports.
//
// NOTE on a deliberate asymmetry with the expander (expand.go): the JS
// token scanner used during actual expansion (`transformLine`) does NOT
// strip inline code spans before scanning for '@' tokens — it scans the raw
// line and happens to treat a backtick run it meets mid-token as ordinary,
// non-whitespace characters. FindImports, by contrast, strips code spans
// first (via splitOutInlineCode) before looking for tokens, matching JS
// findImports. The two can legitimately disagree: for the line "@a.md`x`",
// FindImports yields "a.md" (the span is stripped first) while the expander's
// token scan yields the token "a.md`x`" (backticks are just characters to
// it). This is intentional and faithful to the JS — FindImports is not used
// by the staleness check, so the two functions never need to agree. Do not
// "fix" this by unifying the two scanners.
func FindImports(text string) []string {
	var out []string
	var fence fenceState
	for _, line := range strings.Split(text, "\n") {
		isDelim, inFence := fence.step(line)
		if isDelim || inFence {
			continue
		}
		for _, segment := range splitOutInlineCode(line) {
			out = append(out, collectAtTokens(segment)...)
		}
	}
	return out
}

// splitOutInlineCode returns only the non-code segments of a line, dropping
// inline code spans. A run of n backticks is closed by a plain substring
// search for the next run of n consecutive backticks — this is JS indexOf
// semantics (`line.indexOf(closer, i)`), NOT CommonMark's exact-length rule:
// an n-tick opener is closed by the next occurrence of n consecutive
// backticks even when that occurrence is the head of a longer run. For
// example, "`@a.md ``` @x.md" has its 1-tick opener closed by the first tick
// of the 3-tick run, so only "x.md" survives as code-free text. An
// unterminated run (no matching closer before end of line) is emitted as
// literal backticks and scanning continues after it.
func splitOutInlineCode(line string) []string {
	if strings.IndexByte(line, '`') == -1 {
		return []string{line} // fast path: no backticks, nothing to split
	}
	var segments []string
	var buf strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			buf.WriteByte(line[i])
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		closer := line[start:i] // run of `ticks` backticks
		closeIdx := strings.Index(line[i:], closer)
		if closeIdx == -1 {
			buf.WriteString(closer) // unterminated: literal backticks
			continue
		}
		segments = append(segments, buf.String())
		buf.Reset()
		i += closeIdx + len(closer)
	}
	segments = append(segments, buf.String())
	return segments
}

// collectAtTokens returns the @-tokens in a code-free segment, without the
// leading '@'. A token starts at '@' and runs to the next rune matching
// jsIsSpace (or end of segment), mirroring JS /@(\S+)/g under Unicode mode.
// Scanning is rune-aware so multi-byte runes are never split mid-token and
// non-ASCII whitespace (e.g. NBSP, U+3000, U+FEFF) terminates a token exactly
// where JS does.
func collectAtTokens(segment string) []string {
	var out []string
	for i := 0; i < len(segment); {
		r, size := utf8.DecodeRuneInString(segment[i:])
		if r != '@' {
			i += size
			continue
		}
		start := i
		j := i + size
		for j < len(segment) {
			r2, size2 := utf8.DecodeRuneInString(segment[j:])
			if jsIsSpace(r2) {
				break
			}
			j += size2
		}
		if j > start+size {
			out = append(out, segment[start+size:j])
		}
		i = j
	}
	return out
}
