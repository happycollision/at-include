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
// lines with trailing text after the run of fence characters. This is the
// behavior Claude Code itself uses when inlining @-imports, which is what
// this tool reproduces.
type fenceState struct {
	open bool
	char byte // '`' or '~'
	len  int  // opening run length
}

// fenceDelim returns the fence character and run length if the line is a fence
// delimiter, or ok=false when it is not. Leading whitespace is stripped first,
// so e.g. a tab before the fence characters still counts as indentation.
func fenceDelim(line string) (char byte, runLen int, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
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

// linePiece is one chunk of a line as produced by scanLine: either literal
// text to copy through unchanged (which may be an inline code span, copied
// backticks and all) or an '@' token candidate (Text holds the text after
// the '@', not including it, when IsToken is true).
type linePiece struct {
	Text    string
	IsToken bool
}

// scanLine walks one line (already known not to be a fence delimiter or
// inside a fenced block) and splits it into literal runs and '@' token
// candidates. This is the single token-scanning rule shared by FindImports
// (--list-imports) and the expander's line transform (expand.go's
// transformLine), so the two always agree about which tokens exist in a
// given line.
//
// The rule:
//   - A run of one or more backticks opens an inline code span. It is closed
//     by the next occurrence of the same number of consecutive backticks,
//     found via a plain substring search (not CommonMark's exact-run-length
//     rule — an n-tick opener closes on the first n-tick occurrence even if
//     that's the head of a longer run). If a closer is found, the whole span
//     — both backtick runs and everything between — is one literal piece,
//     and its interior is never scanned for '@' tokens. If no closer is
//     found before end of line, the backtick run itself is emitted as a
//     literal piece and scanning continues normally after it (so a later '@'
//     on the same line is still seen).
//   - Outside a code span, '@' starts a token that runs to the next
//     whitespace rune (unicode.IsSpace) or end of line — including through
//     any backticks encountered along the way, which are just ordinary,
//     non-whitespace characters once a token scan is underway.
//   - Everything else is literal text.
//
// Scan-to-whitespace is the current assumption for where a token ends; it
// does not support a @path containing a space (e.g. via escaping or
// quoting). Whether Claude Code's own @-import handling supports that is a
// separate, open question tracked outside this file — revisit this rule if
// it turns out paths-with-spaces need to work.
func scanLine(line string) []linePiece {
	var pieces []linePiece
	var lit strings.Builder
	flushLiteral := func() {
		if lit.Len() > 0 {
			pieces = append(pieces, linePiece{Text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(line); {
		switch line[i] {
		case '`':
			ticks := 0
			for i < len(line) && line[i] == '`' {
				ticks++
				i++
			}
			closer := strings.Repeat("`", ticks)
			closeIdx := strings.Index(line[i:], closer)
			if closeIdx == -1 {
				lit.WriteString(closer) // unterminated run: literal backticks
				continue
			}
			lit.WriteString(closer + line[i:i+closeIdx] + closer)
			i += closeIdx + ticks
		case '@':
			j := i + 1
			for j < len(line) {
				r, size := utf8.DecodeRuneInString(line[j:])
				if unicode.IsSpace(r) {
					break
				}
				j += size
			}
			flushLiteral()
			pieces = append(pieces, linePiece{Text: line[i+1 : j], IsToken: true})
			i = j
		default:
			r, size := utf8.DecodeRuneInString(line[i:])
			lit.WriteRune(r)
			i += size
		}
	}
	flushLiteral()
	return pieces
}

// FindImports returns the @path candidates in text, in document order and
// including duplicates, skipping fenced code blocks and using the same
// token-scanning rule as the expander (scanLine, above) — so the tokens
// reported here are exactly the tokens the expander would consider for
// replacement. Whether a candidate is a real import is decided later by
// resolving it against the filesystem, which is what keeps email addresses
// and @scope/package mentions from being treated as imports.
func FindImports(text string) []string {
	var out []string
	var fence fenceState
	for _, line := range strings.Split(text, "\n") {
		isDelim, inFence := fence.step(line)
		if isDelim || inFence {
			continue
		}
		for _, piece := range scanLine(line) {
			if piece.IsToken && piece.Text != "" {
				out = append(out, piece.Text)
			}
		}
	}
	return out
}
