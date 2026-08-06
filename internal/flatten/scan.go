// Package flatten implements @path import flattening modeled on what Claude
// Code does when it reads a CLAUDE.md/AGENTS.md file: an @-prefixed token that
// resolves to a real file is replaced by that file's contents, recursively.
//
// The rules here were derived from observed Claude Code behavior and are meant
// to agree with it in intent and in ordinary use, but this is an independent
// implementation rather than a port, and exact parity is neither claimed nor
// guaranteed. Upstream's import handling is largely undocumented and may change
// between releases; known differences and the drift risk are discussed in
// docs/architecture.md. Where the two disagree, this package's tests define the
// behavior.
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
// lines with trailing text after the run of fence characters. The intent is to
// skip anything a reader would recognize as a code fence, erring toward not
// expanding rather than expanding something inside an example block.
//
// Note this is a line-oriented approximation, not the mechanism Claude Code
// uses: upstream runs its import scan over parsed Markdown token nodes and
// skips the ones typed code/codespan. The two agree on ordinary documents, but
// unusual Markdown may partition differently — see docs/architecture.md.
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
// Raw holds the token's original source text after the '@' (escapes and '#'
// fragment intact) when IsToken is true, so the expander can put an
// unresolvable token back byte for byte. Text is the resolution candidate:
// unescaped and fragment-stripped.
type linePiece struct {
	Text    string
	Raw     string
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
//   - Outside a code span, '@' starts a token only when it is at line start or
//     immediately preceded by a whitespace rune (see atTokenBoundary). A '@'
//     anywhere else — mid-word, after punctuation, or directly after a
//     backtick — is ordinary literal text. This is what keeps an email's '@'
//     from yielding a candidate at all.
//   - A token that has opened runs to the next whitespace rune
//     (unicode.IsSpace) or end of line — including through any backticks
//     encountered along the way, which are just ordinary, non-whitespace
//     characters once a token scan is underway.
//   - The first '#' in a token begins a fragment/anchor: it and everything
//     after it are consumed as part of the token's extent but dropped from the
//     resolution candidate, so @a.md#frag resolves a.md.
//   - Within a token, a backslash immediately followed by a space ("\ ")
//     is an escaped space: it does not end the token, and it contributes a
//     plain space to the token text. A backslash in any other position
//     (followed by a non-space, or at end of line) ends the token and is
//     not part of it.
//   - Everything else is literal text.
//
// These rules were modeled on Claude Code's own @-import scanner (as shipped in
// 2.1.221; an undocumented detail that may change), whose token pattern is
//
//	/(?:^|\s)@((?:[^\s\\]|\\ )+)/g
//
// followed, per match, by truncation at the first '#' and then
// replaceAll("\\ ", " ") on the remainder. The (?:^|\s) prefix is the
// token-boundary rule; the [^\s\\]|\\ alternation is why "\ " continues a
// token while a lone backslash terminates it; and the truncate-then-unescape
// order is observable — in "@a\ b#c\ d.md" the '#' is found while the token is
// still escaped, so the candidate is "a b".
//
// Escaping is the *only* supported way to write a @path containing a space:
// quoting is not a mechanism (@"a b.md" yields the token `"a`), a bare space
// always ends the token, and there is no longest-match-on-disk probing.
//
// All of this was verified empirically against Claude Code, not just read off
// the pattern: with a.md, b.md, frag.md and a real file named bar.com all
// present, a CLAUDE.md containing "`@a.md and @b.md", "@frag.md#some-section"
// and "mail foo@bar.com" produced "Contents of ..." markers for exactly b.md
// and frag.md — proving the boundary rule suppresses both the backtick-adjacent
// token and the email even when the email's candidate exists on disk.
//
// One upstream layer is intentionally not reproduced: after truncating and
// unescaping, Claude Code applies acceptance filters to the candidate (it
// rejects paths starting with '@' or with a leading [#%^&*()], and otherwise
// requires a leading [a-zA-Z0-9._-] unless the path starts with "./", "~/", or
// "/"). Here such candidates are simply allowed to fail to resolve, which
// yields the same observable outcome — literal passthrough — without a second
// notion of validity.
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
			// The '@' only opens a token at line start or immediately after
			// whitespace. Anywhere else (mid-word, after punctuation) it is
			// ordinary literal text — which is what keeps an email's '@' from
			// producing a candidate at all.
			if !atTokenBoundary(line, i) {
				lit.WriteByte('@')
				i++
				continue
			}
			var tok strings.Builder
			truncated := false // a '#' was seen: stop adding to tok, keep consuming
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' {
					// "\ " is an escaped space: consume both bytes and emit
					// one plain space. A backslash in any other position
					// ends the token without being part of it.
					if j+1 < len(line) && line[j+1] == ' ' {
						if !truncated {
							tok.WriteByte(' ')
						}
						j += 2
						continue
					}
					break
				}
				r, size := utf8.DecodeRuneInString(line[j:])
				if unicode.IsSpace(r) {
					break
				}
				// The first '#' begins a fragment: it and everything after it
				// are dropped from the resolved path, but still consumed as
				// part of the token's extent so they are not re-scanned as
				// literal text.
				if line[j] == '#' {
					truncated = true
				}
				if !truncated {
					tok.WriteString(line[j : j+size])
				}
				j += size
			}
			flushLiteral()
			pieces = append(pieces, linePiece{
				Text:    tok.String(),
				Raw:     line[i+1 : j],
				IsToken: true,
			})
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

// atTokenBoundary reports whether the '@' at byte offset i may open a token:
// true only at line start or when the immediately preceding rune is
// whitespace, mirroring the (?:^|\s) prefix of Claude Code's import pattern.
//
// The preceding rune is decoded with DecodeLastRuneInString so a multi-byte
// space (NBSP, U+3000) counts as whitespace just as it does when terminating a
// token, rather than being misread as a trailing continuation byte.
func atTokenBoundary(line string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(line[:i])
	return unicode.IsSpace(r)
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
