// Package flatten implements the @path import flattening rules that Claude Code
// applies when it reads a CLAUDE.md/AGENTS.md file: an @-prefixed token that
// resolves to a real file is replaced by that file's contents, recursively.
package flatten

import "strings"

// fenceState tracks an open Markdown fenced code block across lines.
//
// CommonMark rules we reproduce: a fence opens with a run of 3+ backticks or
// tildes (leading whitespace allowed) and is closed only by a run of the same
// character that is at least as long. That means a longer outer fence is not
// closed by a shorter inner one, and a tilde line inside a backtick fence is
// just content.
type fenceState struct {
	open bool
	char byte // '`' or '~'
	len  int  // opening run length
}

// fenceDelim returns the fence character and run length if the line is a fence
// delimiter, or ok=false when it is not.
func fenceDelim(line string) (char byte, runLen int, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) == 0 {
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
// A candidate starts at '@' and runs to the next whitespace character; the '@'
// may appear anywhere in a line. Whether a candidate is a real import is decided
// later by resolving it against the filesystem, which is what keeps email
// addresses and @scope/package mentions from being treated as imports.
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
// inline code spans. A run of n backticks is closed by the next run of exactly
// n backticks; an unterminated run is emitted as literal backticks and scanning
// continues after it.
func splitOutInlineCode(line string) []string {
	var segments []string
	var buf strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			buf.WriteByte(line[i])
			i++
			continue
		}
		ticks := 0
		for i < len(line) && line[i] == '`' {
			ticks++
			i++
		}
		closer := strings.Repeat("`", ticks)
		closeIdx := strings.Index(line[i:], closer)
		if closeIdx == -1 {
			buf.WriteString(closer) // unterminated: literal backticks
			continue
		}
		segments = append(segments, buf.String())
		buf.Reset()
		i += closeIdx + ticks
	}
	segments = append(segments, buf.String())
	return segments
}

// collectAtTokens returns the @-tokens in a code-free segment, without the '@'.
func collectAtTokens(segment string) []string {
	var out []string
	for i := 0; i < len(segment); i++ {
		if segment[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(segment) && !isSpace(segment[j]) {
			j++
		}
		if j > i+1 {
			out = append(out, segment[i+1:j])
		}
		i = j - 1
	}
	return out
}

// isSpace matches the characters JavaScript's \s matches for the ASCII range we
// care about in Markdown source.
func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}
