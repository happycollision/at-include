package flatten

import (
	"os"
	"path/filepath"
	"regexp"
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

// --- Differential tests against the JS reference implementation ---
//
// The JS build-agents.mjs firstDiffExcerpt/assembleOutput are not exported, so
// these cases were captured by copying both functions verbatim into a scratch
// .mjs (unchanged) and running it under node against a battery of
// (actual, expected) / content inputs. See the task report for the exact
// script. Each want value below is the JS output captured byte-for-byte.

// diffJSCases pins Go firstDiffExcerpt output against the JS reference
// implementation. Each entry is an (actual, expected) pair together with the
// JS firstDiffExcerpt output for that pair.
var diffJSCases = []struct{ actual, expected, want string }{
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

func TestFirstDiffExcerptMatchesJS(t *testing.T) {
	t.Parallel()
	for _, c := range diffJSCases {
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

// assembleTrimJSCases pins the JS assembleOutput's trailing-newline
// normalization (`(prefix).replace(/\n*$/, "\n")`) against Go's
// strings.TrimRight(prefix, "\n") + "\n" directly on a battery of raw
// "banner\n\ncontent" strings, independent of the (deliberately different)
// banner text. Captured from the JS reference by evaluating
// `renderBanner() + "\n\n" + content` through `.replace(/\n*$/, "\n")` for
// each content value below.
var assembleTrimJSCases = []struct{ content, wantTail string }{
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

// trailingNewlineRunRe mirrors the JS regex /\n*$/ used by assembleOutput.
var trailingNewlineRunRe = regexp.MustCompile(`\n*$`)

// jsReplaceTrailingNewlines reproduces JS's `s.replace(/\n*$/, "\n")` exactly:
// replace the (possibly empty) maximal trailing run of "\n" with a single "\n".
func jsReplaceTrailingNewlines(s string) string {
	loc := trailingNewlineRunRe.FindStringIndex(s)
	return s[:loc[0]] + "\n"
}

// TestAssembleMatchesJS verifies, on every case, that Go's
// strings.TrimRight(s, "\n") + "\n" (what Assemble uses) and a direct
// translation of the JS regex replace/\n*$/ produce byte-identical results,
// for a battery of banner+content combinations including empty content,
// all-newline content, content with no trailing newline, and content with
// many trailing newlines.
func TestAssembleMatchesJS(t *testing.T) {
	t.Parallel()
	const banner = "> [!IMPORTANT]\n...banner..."
	for _, c := range assembleTrimJSCases {
		c := c
		t.Run("", func(t *testing.T) {
			t.Parallel()
			raw := banner + "\n\n" + c.content
			goResult := strings.TrimRight(raw, "\n") + "\n"
			jsResult := jsReplaceTrailingNewlines(raw)
			if goResult != jsResult {
				t.Errorf("content %q: TrimRight-based result diverges from JS replace(/\\n*$/,\"\\n\") result\nGo:  %q\nJS:  %q", c.content, goResult, jsResult)
			}
			if !strings.HasSuffix(goResult, c.wantTail) {
				t.Errorf("content %q: result %q does not end with expected tail %q", c.content, goResult, c.wantTail)
			}
			// Also exercise the real Assemble function to confirm it matches the
			// same trim-based computation for this banner/content pair.
			if got := Assemble(banner, c.content); got != goResult {
				t.Errorf("Assemble(%q, %q) = %q, want %q", banner, c.content, got, goResult)
			}
		})
	}
}
