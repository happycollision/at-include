package flatten

import (
	"slices"
	"strings"
	"testing"
)

// Non-ASCII test bytes are built via \u escapes (rather than literal source
// bytes) so the exact code points are unambiguous.
const (
	nbsp      = " " // NBSP: unicode.IsSpace, terminates a token
	ideoSpace = "　" // IDEOGRAPHIC SPACE: unicode.IsSpace, terminates a token
	eAcute    = "é" // e-acute
)

func TestFindImports(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"bare path on its own line", "@PROJECT_MEMORY/index.md", []string{"PROJECT_MEMORY/index.md"}},
		{"anywhere in a line", "See @README for details and @docs/x.md too", []string{"README", "docs/x.md"}},
		{"ignores an inline code span that starts before the @", "An `@app-variants/astro` mention", nil},
		{
			"ignores fenced block",
			strings.Join([]string{"before", "```", "@not/an/import.md", "```", "after @real.md"}, "\n"),
			[]string{"real.md"},
		},
		{"ignores tilde fenced block", strings.Join([]string{"~~~", "@nope.md", "~~~"}, "\n"), nil},
		// The '@' must sit at line start or immediately after whitespace, so an
		// email's '@' (preceded by a word character) yields no candidate at
		// all — matching Claude Code's (?:^|\s)@ boundary.
		{"email yields no candidate at all", "mail foo@bar.com", nil},
		{"@ after a word character is not a token", "a@b.md", nil},
		{"@ after punctuation is not a token", "(@a.md", nil},
		{"@ at line start is a token", "@a.md", []string{"a.md"}},
		{"@ after a tab is a token", "\t@a.md", []string{"a.md"}},
		{"@ after a newline-adjacent space is a token", "x @a.md", []string{"a.md"}},
		{"second @ in a run is not its own token", "@@a.md", []string{"@a.md"}},
		{"token stops at whitespace, keeps punctuation", "@a/b.md, next", []string{"a/b.md,"}},

		// A '#' truncates the token: everything from the first '#' onward is a
		// fragment/anchor and is dropped before resolution, so @a.md#frag
		// resolves a.md — matching Claude Code's indexOf("#") truncation.
		{"fragment suffix is truncated", "@a.md#section", []string{"a.md"}},
		{"only the first # matters", "@a.md#a#b", []string{"a.md"}},
		{"fragment truncation composes with unescaping", "@my\\ notes/f.md#frag", []string{"my notes/f.md"}},
		// Truncation runs on the still-escaped token, before "\ " is
		// unescaped, so an escaped space *after* the '#' is discarded along
		// with the rest of the fragment. This case is what distinguishes the
		// two possible orderings; upstream truncates first.
		{"truncation precedes unescaping", "@a\\ b#c\\ d.md", []string{"a b"}},
		{"a token that is only a fragment yields nothing", "@#frag", nil},
		{"# inside a fenced block is still skipped", "```\n@a.md#f\n```", nil},
		{
			"longer outer fence not closed by shorter inner fence",
			strings.Join([]string{
				"````markdown", "```", "@nested-example.md", "```",
				"@should-stay-hidden.md", "````", "after @real.md",
			}, "\n"),
			[]string{"real.md"},
		},
		{
			"differently-charred fence line inside an open fence is swallowed",
			strings.Join([]string{"```", "~~~", "@a.md", "```", "@b.md"}, "\n"),
			[]string{"b.md"},
		},
		// An unterminated backtick run is emitted as literal text and scanning
		// continues, but the '@' directly after it is no longer at a token
		// boundary, so only the later whitespace-preceded '@' is a candidate.
		// Verified against Claude Code: with a.md and b.md both present, only
		// b.md is inlined.
		{"unterminated backtick run keeps scanning", "`@a.md and @b.md", []string{"b.md"}},
		{"double-backtick span needs a double closer", "``@a.md `@b.md` @c.md``", nil},
		{"indented fence still opens", strings.Join([]string{"  ```", "@a.md", "  ```"}, "\n"), nil},
		{"bare @ with nothing after it yields nothing", "@ and @", nil},
		{"duplicates are preserved in document order", "@a.md @b.md @a.md", []string{"a.md", "b.md", "a.md"}},

		// A token is not stopped by backticks it runs into: once a token
		// scan is underway (because the '@' came first, with no preceding
		// backtick), a backtick is just an ordinary, non-whitespace
		// character — matching the expander's transformLine exactly (see
		// scanLine's doc comment in scan.go). This is the case that used to
		// make `imports` disagree with expansion. Compare with "ignores
		// an inline code span that starts before the @" above, and see the
		// fenced-and-inline-code fixture / TestFlattenPreservesFencedAndInlineCode
		// for the corresponding expansion-side behavior with a *bare*
		// @token next to (not touching) a code span.
		{"a token running into a code span is not stopped by it", "@a.md`x` and @b.md", []string{"a.md`x`", "b.md"}},
		{"@ immediately followed by a code span runs through it", "@`x`", []string{"`x`"}},

		// Ordinary whitespace parity (space, tab, and common non-ASCII
		// space characters unicode.IsSpace also matches).
		{"NBSP terminates a token", "@a.md" + nbsp + "@b.md", []string{"a.md", "b.md"}},
		{"U+3000 (ideographic space) terminates a token", "@a.md" + ideoSpace + "@b.md", []string{"a.md", "b.md"}},
		{
			"multi-byte non-space runes are preserved in a token",
			"@caf" + eAcute + "/r" + eAcute + "sum" + eAcute + ".md",
			[]string{"caf" + eAcute + "/r" + eAcute + "sum" + eAcute + ".md"},
		},

		// Backslash-escaped spaces: the one mechanism Claude Code provides
		// for a @path containing a space. A "\ " pair continues the token
		// and is unescaped to a plain space in the reported candidate; a
		// bare (unescaped) space still ends the token.
		{"escaped space continues a token", "@my\\ notes/file.md", []string{"my notes/file.md"}},
		{
			"multiple escaped spaces in one token",
			"@a\\ b\\ c/file.md",
			[]string{"a b c/file.md"},
		},
		{
			"escaped-space token still ends at the next bare space",
			"@my\\ notes/file.md and more",
			[]string{"my notes/file.md"},
		},
		{
			"escaped space mid-line among other tokens",
			"see @a\\ b.md then @c.md",
			[]string{"a b.md", "c.md"},
		},
		{"bare space still ends a token (unescaped)", "@my notes/file.md", []string{"my"}},
		{"double quotes are not an escaping mechanism", `@"q notes/file.md"`, []string{`"q`}},
		{"single quotes are not an escaping mechanism", "@'q notes/file.md'", []string{"'q"}},
		{
			"a backslash not followed by a space ends the token",
			"@a\\b.md",
			[]string{"a"},
		},
		{
			"trailing backslash at end of line ends the token",
			"@a.md\\",
			[]string{"a.md"},
		},
		{
			"escaped space immediately after the @ is kept",
			"@\\ leading.md",
			[]string{" leading.md"},
		},

		{"fence opened but never closed at EOF swallows everything after it", "```\n@hidden.md\n", nil},
		{"fence opener with an info string still opens the fence", "```go\n@hidden.md\n```", nil},
		{
			"closer run longer than the opener still closes (n >= f.len)",
			"```\n@hidden.md\n````",
			nil,
		},
		{"tab-indented fence still opens", "\t```\n@a.md\n\t```", nil},
		{
			"CRLF line endings: fence still opens/closes and trailing \\r ends a token",
			"before\r\n```\r\n@hidden.md\r\n```\r\nafter @real.md\r\n",
			[]string{"real.md"},
		},
		{"empty input yields nothing", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FindImports(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("FindImports(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
