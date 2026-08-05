package flatten

import (
	"slices"
	"strings"
	"testing"
)

// Non-ASCII test bytes are built via \u escapes (rather than literal source
// bytes) so the exact code points are unambiguous — a literal U+FEFF, for
// instance, is only legal at the very start of a Go source file, and it's
// easy to typo an invisible character when it's pasted in as-is.
const (
	nbsp      = "\u00A0" // NBSP: JS \s, terminates a token
	ideoSpace = "\u3000" // IDEOGRAPHIC SPACE: JS \s, terminates a token
	bom       = "\uFEFF" // BOM/ZWNBSP: JS \s DOES match; unicode.IsSpace does NOT
	nel       = "\u0085" // NEL: unicode.IsSpace DOES match; JS \s does NOT
	eAcute    = "\u00E9" // e-acute
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
		{"ignores inline code span", "An `@app-variants/astro` mention", nil},
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
		// Boundary cases beyond the JS suite:
		{"unterminated backtick run keeps scanning", "`@a.md and @b.md", []string{"a.md", "b.md"}},
		{"double-backtick span needs a double closer", "``@a.md `@b.md` @c.md``", nil},
		{"indented fence still opens", strings.Join([]string{"  ```", "@a.md", "  ```"}, "\n"), nil},
		{"bare @ with nothing after it yields nothing", "@ and @", nil},
		{"duplicates are preserved in document order", "@a.md @b.md @a.md", []string{"a.md", "b.md", "a.md"}},

		// Unicode whitespace parity with JS \s (verified against the JS oracle
		// by running node against build-agents.mjs's findImports).
		{"NBSP terminates a token", "@a.md" + nbsp + "@b.md", []string{"a.md", "b.md"}},
		{"U+3000 (ideographic space) terminates a token", "@a.md" + ideoSpace + "@b.md", []string{"a.md", "b.md"}},
		{"U+FEFF (BOM/ZWNBSP) terminates a token", "@a.md" + bom + "@b.md", []string{"a.md", "b.md"}},
		{
			"U+0085 (NEL) does NOT terminate a token (the unicode.IsSpace delta)",
			"@a.md" + nel + "@b.md",
			[]string{"a.md" + nel + "@b.md"},
		},
		{"form-feed-indented fence opens the fence", "\f```\n@hidden.md\n```", nil},
		{
			"multi-byte non-space runes are preserved in a token",
			"@caf" + eAcute + "/r" + eAcute + "sum" + eAcute + ".md",
			[]string{"caf" + eAcute + "/r" + eAcute + "sum" + eAcute + ".md"},
		},

		// Missing state-machine coverage (Fix 3). Every "want" below was
		// obtained by running the JS findImports on the same input.
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
		{"@ immediately followed by a code span yields nothing", "@`x`", nil},
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
