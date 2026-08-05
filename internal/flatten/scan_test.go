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
		{"email is a candidate by its @-token", "mail foo@bar.com", []string{"bar.com"}},
		{"token stops at whitespace, keeps punctuation", "@a/b.md, next", []string{"a/b.md,"}},
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
		{"unterminated backtick run keeps scanning", "`@a.md and @b.md", []string{"a.md", "b.md"}},
		{"double-backtick span needs a double closer", "``@a.md `@b.md` @c.md``", nil},
		{"indented fence still opens", strings.Join([]string{"  ```", "@a.md", "  ```"}, "\n"), nil},
		{"bare @ with nothing after it yields nothing", "@ and @", nil},
		{"duplicates are preserved in document order", "@a.md @b.md @a.md", []string{"a.md", "b.md", "a.md"}},

		// A token is not stopped by backticks it runs into: once a token
		// scan is underway (because the '@' came first, with no preceding
		// backtick), a backtick is just an ordinary, non-whitespace
		// character — matching the expander's transformLine exactly (see
		// scanLine's doc comment in scan.go). This is the case that used to
		// make --list-imports disagree with expansion. Compare with "ignores
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
