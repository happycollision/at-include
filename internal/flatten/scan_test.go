package flatten

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindImports(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindImports(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindImports(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
