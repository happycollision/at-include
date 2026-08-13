package flatten

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBannerMentionsSourceAndAgentRule(t *testing.T) {
	t.Parallel()
	b := Banner(Options{SrcName: "AGENTS.src.md", OutName: "AGENTS.md"})
	for _, want := range []string{
		"> [!IMPORTANT]",
		"This file is generated",
		"AGENTS.src.md",
		"at-include",
		"If you are an agent",
		"AGENTS.md",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("banner missing %q\ngot:\n%s", want, b)
		}
	}
	if strings.HasSuffix(b, "\n") {
		t.Error("banner should not end with a newline; Assemble adds the separator")
	}
}

func TestBannerUsesCustomNames(t *testing.T) {
	t.Parallel()
	b := Banner(Options{SrcName: "CLAUDE.src.md", OutName: "CLAUDE.md"})
	if !strings.Contains(b, "CLAUDE.src.md") || !strings.Contains(b, "CLAUDE.md") {
		t.Errorf("banner should use the configured names\ngot:\n%s", b)
	}
	if strings.Contains(b, "AGENTS") {
		t.Errorf("banner should not hardcode AGENTS\ngot:\n%s", b)
	}
}

func TestGenerateStructureAndTrailingNewline(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Body line\n\n@x.md\n",
		"x.md":          "X-CONTENT\n",
	})
	out, err := Generate(optsFor(root))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(out, "> [!IMPORTANT]") {
		t.Errorf("output should start with the callout\ngot:\n%s", out)
	}
	for _, want := range []string{"Body line", "X-CONTENT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("output must end with exactly one newline, got %q", tail(out))
	}
}

func TestGenerateTrailingNewlineInvariantForDegenerateSources(t *testing.T) {
	t.Parallel()
	for _, src := range []string{"", "\n\n\n", "Body", "Body\n\n\n"} {
		src := src
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			root := makeTree(t, map[string]string{"AGENTS.src.md": src})
			out, err := Generate(optsFor(root))
			if err != nil {
				t.Fatalf("Generate(%q): %v", src, err)
			}
			if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
				t.Errorf("src %q: want exactly one trailing newline, got %q", src, tail(out))
			}
		})
	}
}

func tail(s string) string {
	if len(s) > 8 {
		return s[len(s)-8:]
	}
	return s
}

func TestGenerateSupplementUsesPreambleNotBanner(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.md": "Body line\n\n@x.md\n",
		"x.md":      "X-CONTENT\n",
	})
	opts := optsFor(root)
	opts.SrcPath = filepath.Join(root, "AGENTS.md")
	opts.Supplement = true
	out, err := Generate(opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(out, "This file is generated") {
		t.Errorf("supplement output should not contain the normal banner\ngot:\n%s", out)
	}
	if !strings.Contains(out, "pre-expanded") {
		t.Errorf("supplement output should contain the supplement preamble\ngot:\n%s", out)
	}
	for _, want := range []string{"Body line", "X-CONTENT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestCheckUpToDate(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	opts := optsFor(root)
	out, err := Generate(opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(out), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res, err := Check(opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.UpToDate {
		t.Errorf("want UpToDate, got %+v", res)
	}
}

func TestCheckStaleIncludesDiffExcerpt(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n", "AGENTS.md": "stale contents\n",
	})
	res, err := Check(optsFor(root))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.UpToDate {
		t.Error("want stale")
	}
	if res.DiffExcerpt == "" {
		t.Error("want a non-empty diff excerpt")
	}
	if !strings.Contains(res.DiffExcerpt, "First difference around line") {
		t.Errorf("excerpt should name the line\ngot:\n%s", res.DiffExcerpt)
	}
}

func TestCheckMissingOutputIsStaleNotAnError(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	res, err := Check(optsFor(root))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.UpToDate || !res.Missing {
		t.Errorf("want stale+missing, got %+v", res)
	}
}

// firstDiffExcerptCases pins firstDiffExcerpt's exact output — including the
// "First difference around line N" wording, the two-line context window, and
// the "  "/"- "/"+ " line prefixes — as golden values. These are not derived
// from any formula in the test; they're the literal expected rendering, so a
// change to any of them means the excerpt format changed and the fixture
// suite (and any tooling that parses check output) would need updating.
var firstDiffExcerptCases = []struct{ actual, expected, want string }{
	{"a\nb\nc", "a\nb\nc", "First difference around line 4:\n  b\n  c"},
	{"X\nb\nc", "a\nb\nc", "First difference around line 1:\n- X\n+ a\n  b\n  c"},
	{"a\nb\nZZZ\nd\ne", "a\nb\nc\nd\ne", "First difference around line 3:\n  a\n  b\n- ZZZ\n+ c\n  d\n  e"},
	{"a\nb\nc\nZ", "a\nb\nc\nd", "First difference around line 4:\n  b\n  c\n- Z\n+ d"},
	{"a\nb\nc", "a\nb\nc\nd\ne", "First difference around line 4:\n  b\n  c\n+ d\n+ e"},
	{"a\nb\nc\nd\ne", "a\nb\nc", "First difference around line 4:\n  b\n  c\n- d\n- e"},
	{"", "", "First difference around line 2:\n  "},
	{"", "a\nb", "First difference around line 1:\n- \n+ a\n+ b"},
	{"a\nb", "", "First difference around line 1:\n- a\n+ \n- b"},
	{"a", "b", "First difference around line 1:\n- a\n+ b"},
	{"a", "a", "First difference around line 2:\n  a"},
	{"a\nb\n", "a\nb", "First difference around line 3:\n  a\n  b\n- "},
	{"a\nb", "a\nb\n", "First difference around line 3:\n  a\n  b\n+ "},
	{"a\nb\n\n", "a\nb\n", "First difference around line 4:\n  b\n  \n- "},
	{"1\n2\n3\n4\n5\n6\nDIFF\n8", "1\n2\n3\n4\n5\n6\n7\n8", "First difference around line 7:\n  5\n  6\n- DIFF\n+ 7\n  8"},
	{"DIFF\n2\n3\n4\n5", "1\n2\n3\n4\n5", "First difference around line 1:\n- DIFF\n+ 1\n  2\n  3"},
	{"a\nb\nc\nd", "a\nb\nc\nd\ne\nf", "First difference around line 5:\n  c\n  d\n+ e\n+ f"},
	{"a\nb\nc\nd\ne\nf", "a\nb\nc\nd", "First difference around line 5:\n  c\n  d\n- e\n- f"},
	{"foo", "bar", "First difference around line 1:\n- foo\n+ bar"},
	{"\n\n\n", "\n\n\n\n", "First difference around line 5:\n  \n  \n+ "},
	{"\n\n\n\n", "\n\n\n", "First difference around line 5:\n  \n  \n- "},
	{"z\nz\nz\nz\nz\nz\nz\nz\nz\nz", "y\ny\ny", "First difference around line 1:\n- z\n+ y\n- z\n+ y\n- z\n+ y"},
}

func TestFirstDiffExcerpt(t *testing.T) {
	t.Parallel()
	for _, c := range firstDiffExcerptCases {
		c := c
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := firstDiffExcerpt(c.actual, c.expected)
			if got != c.want {
				t.Errorf("firstDiffExcerpt(%q, %q)\ngot:  %q\nwant: %q", c.actual, c.expected, got, c.want)
			}
		})
	}
}

// assembleTrailingNewlineCases pins Assemble's trailing-newline normalization
// on a battery of content values — empty, all-newline, no trailing newline,
// and many trailing newlines — independent of the (unrelated) banner text.
// Each wantTail is what the assembled "banner\n\ncontent" string must end
// with once normalized to exactly one trailing newline.
var assembleTrailingNewlineCases = []struct{ content, wantTail string }{
	{"", "\n"},
	{"\n", "\n"},
	{"\n\n\n", "\n"},
	{"Body", "\nBody\n"},
	{"Body\n", "\nBody\n"},
	{"Body\n\n\n", "\nBody\n"},
	{"Body\n\n\nTail", "\nBody\n\n\nTail\n"},
	{"   ", "\n   \n"},
	{"\n\n\nBody\n\n\n", "\n\n\n\nBody\n"},
	{"a\nb\nc", "\na\nb\nc\n"},
}

// TestAssembleTrailingNewline verifies, for every case, that Assemble collapses
// however many trailing newlines are present in banner+content down to
// exactly one, regardless of whether content is empty, all newlines, missing
// a trailing newline, or has several.
func TestAssembleTrailingNewline(t *testing.T) {
	t.Parallel()
	const banner = "> [!IMPORTANT]\n...banner..."
	for _, c := range assembleTrailingNewlineCases {
		c := c
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := Assemble(banner, c.content)
			if !strings.HasSuffix(got, c.wantTail) {
				t.Errorf("Assemble(banner, %q) = %q, want suffix %q", c.content, got, c.wantTail)
			}
			if strings.HasSuffix(got, "\n\n") {
				t.Errorf("Assemble(banner, %q) = %q, has more than one trailing newline", c.content, got)
			}
		})
	}
}
