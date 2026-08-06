package flatten

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTree writes files (keyed by slash-separated relative path) into a fresh
// temp dir and returns its path. t.TempDir cleans up automatically.
func makeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	// Resolve symlinks so markers computed from RootDir match on macOS, where
	// /var and /tmp are symlinks into /private.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return dir
}

// optsFor deliberately leaves MarkerDesc unset (zero value) so the whole
// suite exercises markerDesc()'s default-branch behavior; that default value
// equals DefaultMarkerDesc, so existing assertions are unaffected.
// TestFlattenCustomMarkerDesc separately pins the override path.
func optsFor(root string) Options {
	return Options{
		SrcPath: filepath.Join(root, "AGENTS.src.md"),
		OutPath: filepath.Join(root, "AGENTS.md"),
		RootDir: root,
		SrcName: "AGENTS.src.md",
		OutName: "AGENTS.md",
	}
}

func TestFlattenInlinesSingleImport(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md":           "Top\n\n@PROJECT_MEMORY/index.md\n\nBottom\n",
		"PROJECT_MEMORY/index.md": "MEMORY CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{
		"Top\n",
		"Contents of PROJECT_MEMORY/index.md (project instructions, checked into the codebase):",
		"MEMORY CONTENT",
		"Bottom",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q\ngot:\n%s", want, content)
		}
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
}

func TestFlattenResolvesNestedRelativeToImporter(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md":           "@PROJECT_MEMORY/index.md\n",
		"PROJECT_MEMORY/index.md": "INDEX\n\n@leaf.md\n",
		"PROJECT_MEMORY/leaf.md":  "LEAF\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{
		"INDEX", "LEAF",
		"Contents of PROJECT_MEMORY/leaf.md (project instructions, checked into the codebase):",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q\ngot:\n%s", want, content)
		}
	}
	if inlined != 2 {
		t.Errorf("inlined = %d, want 2", inlined)
	}
}

func TestFlattenLeavesUnresolvableTokensLiteral(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Use @app-variants/astro and mail foo@bar.com\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{"@app-variants/astro", "foo@bar.com"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q\ngot:\n%s", want, content)
		}
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0", inlined)
	}
}

// TestFlattenEmailNeverImportsEvenWhenCandidateFileExists is the case that
// proves the token-boundary rule does real work rather than being masked by
// paths that happen not to resolve: "bar.com" exists here as a regular file,
// and it still must not be inlined, because the '@' in foo@bar.com follows a
// word character. Verified against Claude Code with the same setup — it emits
// no "Contents of .../bar.com" marker.
func TestFlattenEmailNeverImportsEvenWhenCandidateFileExists(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Reach out to foo@bar.com for questions.\n",
		"bar.com":       "EMAIL-CANDIDATE-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0", inlined)
	}
	if strings.Contains(content, "EMAIL-CANDIDATE-CONTENT") {
		t.Errorf("email inlined a real file it must not touch\ngot:\n%s", content)
	}
	if content != "Reach out to foo@bar.com for questions.\n" {
		t.Errorf("content = %q, want it unchanged", content)
	}
}

// TestFlattenFragmentSuffixResolvesBasePath pins '#' truncation end to end: the
// fragment is dropped for resolution, the base file is inlined, and the
// fragment text does not survive in the output (it was consumed as part of the
// token's extent).
func TestFlattenFragmentSuffixResolvesBasePath(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "See @guide.md#setup for details\n",
		"guide.md":      "GUIDE-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	if !strings.Contains(content, "GUIDE-CONTENT") {
		t.Errorf("content missing inlined body\ngot:\n%s", content)
	}
	if !strings.Contains(content, "Contents of guide.md") {
		t.Errorf("marker should name the base path, not the fragment\ngot:\n%s", content)
	}
	if strings.Contains(content, "#setup") {
		t.Errorf("fragment text should not survive in output\ngot:\n%s", content)
	}
}

// An unresolvable token must be written back byte for byte, fragment and
// escapes intact — the scanner strips both to build the resolution candidate,
// so the expander relies on linePiece.Raw to avoid silently rewriting prose
// that was never an import.
func TestFlattenUnresolvableTokenPreservesFragmentAndEscapes(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"See @nope.md#setup here\n",
		"See @no\\ such/file.md#frag here\n",
		"Bare @nope.md#a#b here\n",
	} {
		root := makeTree(t, map[string]string{"AGENTS.src.md": line})
		content, inlined, err := Flatten(optsFor(root))
		if err != nil {
			t.Fatalf("Flatten: %v", err)
		}
		if inlined != 0 {
			t.Errorf("inlined = %d, want 0", inlined)
		}
		if content != line {
			t.Errorf("content = %q, want it unchanged (%q)", content, line)
		}
	}
}

func TestFlattenInlinesEscapedSpacePath(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md":    "Top\n\n@my\\ notes/file.md\n\nBottom\n",
		"my notes/file.md": "SPACED CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(content, "SPACED CONTENT") {
		t.Errorf("content missing inlined body\ngot:\n%s", content)
	}
	// The marker renders the real (unescaped) path, matching the file on disk.
	if !strings.Contains(content, "Contents of my notes/file.md") {
		t.Errorf("content missing unescaped marker path\ngot:\n%s", content)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
}

// An escaped-space token that does not resolve must be written back exactly as
// authored, backslash included — the scanner unescapes the token text, so the
// expander has to re-escape when it puts the literal back.
func TestFlattenLeavesUnresolvableEscapedSpaceTokenVerbatim(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Use @no\\ such/file.md here\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if want := "Use @no\\ such/file.md here"; !strings.Contains(content, want) {
		t.Errorf("content missing %q\ngot:\n%s", want, content)
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0", inlined)
	}
}

func TestFlattenDiamondEmitsSingleCopy(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n\n@b.md\n",
		"a.md":          "A\n\n@shared.md\n",
		"b.md":          "B\n\n@shared.md\n",
		"shared.md":     "SHARED-UNIQUE-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if n := strings.Count(content, "SHARED-UNIQUE-CONTENT"); n != 1 {
		t.Errorf("shared content appears %d times, want 1", n)
	}
	if !strings.Contains(content, "Contents of shared.md (already inlined above):") {
		t.Errorf("missing already-inlined marker\ngot:\n%s", content)
	}
	if inlined != 3 {
		t.Errorf("inlined = %d, want 3", inlined)
	}
}

// TestFlattenGoldenDiamond pins the diamond case's EXACT full output, not just
// substring/count checks, so a check.go-style byte-exact comparison would
// actually catch a regression in where the "already inlined above" marker
// lands relative to the surrounding content. This is a golden-output test:
// the want string is the pinned expected rendering, not derived from a
// formula — if it fails, the renderer changed and either the code or this
// expectation needs to be fixed.
func TestFlattenGoldenDiamond(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n\n@b.md\n",
		"a.md":          "A\n\n@shared.md\n",
		"b.md":          "B\n\n@shared.md\n",
		"shared.md":     "SHARED-UNIQUE-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 3 {
		t.Errorf("inlined = %d, want 3", inlined)
	}
	want := "Contents of a.md (project instructions, checked into the codebase):\n" +
		"\n" +
		"A\n" +
		"\n" +
		"Contents of shared.md (project instructions, checked into the codebase):\n" +
		"\n" +
		"SHARED-UNIQUE-CONTENT\n" +
		"\n" +
		"\n" +
		"\n" +
		"Contents of b.md (project instructions, checked into the codebase):\n" +
		"\n" +
		"B\n" +
		"\n" +
		"Contents of shared.md (already inlined above):\n" +
		"\n"
	if content != want {
		t.Errorf("content = %q\nwant      %q", content, want)
	}
}

func TestFlattenTerminatesOnCycle(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n",
		"a.md":          "A\n\n@b.md\n",
		"b.md":          "B\n\n@a.md\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{"A", "B", "Contents of a.md (already inlined above):"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q\ngot:\n%s", want, content)
		}
	}
	if inlined != 2 {
		t.Errorf("inlined = %d, want 2", inlined)
	}
}

func TestFlattenMaxDepthExceeded(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n", "a.md": "@b.md\n", "b.md": "@c.md\n", "c.md": "DEEP\n",
	})
	opts := optsFor(root)
	opts.MaxDepth, opts.MaxDepthSet = 2, true
	_, _, err := Flatten(opts)
	if err == nil {
		t.Fatal("Flatten: want depth error, got nil")
	}
	// Asserting the exact string (not a substring match) is what makes a
	// wording or capitalization regression here impossible to miss.
	want := "import depth 3 exceeds --max-depth 2 at c.md"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestFlattenMaxDepthZero pins the MaxDepth: 0 boundary: a single import puts
// stack length at 1, which exceeds a max of 0.
func TestFlattenMaxDepthZero(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n",
		"a.md":          "CONTENT\n",
	})
	opts := optsFor(root)
	opts.MaxDepth, opts.MaxDepthSet = 0, true
	_, _, err := Flatten(opts)
	if err == nil {
		t.Fatal("Flatten: want depth error, got nil")
	}
	want := "import depth 1 exceeds --max-depth 0 at a.md"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestFlattenMaxDepthBoundaryAllowed(t *testing.T) {
	t.Parallel()
	// src(0) -> a(1) -> b(2): the deepest inline happens at stack length 2,
	// which must NOT fail at MaxDepth 2. Pins a > vs >= regression.
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n", "a.md": "@b.md\n", "b.md": "DEEP-OK\n",
	})
	opts := optsFor(root)
	opts.MaxDepth, opts.MaxDepthSet = 2, true
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(content, "DEEP-OK") {
		t.Errorf("content missing DEEP-OK\ngot:\n%s", content)
	}
	if inlined != 2 {
		t.Errorf("inlined = %d, want 2", inlined)
	}
}

func TestFlattenPreservesFencedAndInlineCode(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": strings.Join([]string{
			"```", "@real.md", "```", "and `@real.md` and @real.md",
		}, "\n"),
		"real.md": "REAL\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1 (only the bare token expands)", inlined)
	}
	if n := strings.Count(content, "REAL\n"); n != 1 {
		t.Errorf("REAL appears %d times, want 1", n)
	}
	if !strings.Contains(content, "and `@real.md` and Contents of real.md") {
		t.Errorf("code span should stay verbatim\ngot:\n%s", content)
	}
}

// TestFlattenUnterminatedBacktickStillExpandsToken covers the least obvious
// branch in transformLine (expand.go's closeIdx == -1 case): an unmatched
// backtick run is emitted literally as plain text, and scanning continues so
// a subsequent @token on the same line still expands normally. The want
// string is the pinned golden output for this input.
//
// The @token is separated from the backtick run by a space on purpose: a '@'
// sitting directly against the backtick would not be at a token boundary (see
// atTokenBoundary in scan.go), which would test the boundary rule instead of
// the unterminated-run branch this case exists to cover.
func TestFlattenUnterminatedBacktickStillExpandsToken(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "` @a.md and more\n",
		"a.md":          "A-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	want := "` Contents of a.md (project instructions, checked into the codebase):\n" +
		"\n" +
		"A-CONTENT\n" +
		" and more\n"
	if content != want {
		t.Errorf("content = %q\nwant      %q", content, want)
	}
}

// TestFlattenBacktickAdjacentTokenStaysLiteral is the companion to the case
// above: with the space removed, the '@' is no longer at a token boundary, so
// nothing expands even though a.md exists. Verified against Claude Code, which
// likewise inlines nothing for "`@a.md".
func TestFlattenBacktickAdjacentTokenStaysLiteral(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "`@a.md and more\n",
		"a.md":          "A-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0", inlined)
	}
	if content != "`@a.md and more\n" {
		t.Errorf("content = %q, want it unchanged", content)
	}
}

func TestFlattenDirectoryTokenStaysLiteral(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@subdir and @subdir/f.md\n",
		"subdir/f.md":   "F\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1 (a directory is not a file)", inlined)
	}
	if !strings.HasPrefix(content, "@subdir and Contents of subdir/f.md") {
		t.Errorf("directory token should stay literal\ngot:\n%s", content)
	}
}

func TestFlattenMissingSourceIsAnError(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{"other.md": "x\n"})
	if _, _, err := Flatten(optsFor(root)); err == nil {
		t.Fatal("Flatten: want error for missing source, got nil")
	}
}

func TestFlattenSelfImportTerminates(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "Top\n@AGENTS.src.md\nBottom\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	if !strings.Contains(content, "Contents of AGENTS.src.md (already inlined above):") {
		t.Errorf("missing already-inlined marker for self-import\ngot:\n%s", content)
	}
}

func TestFlattenTrailingCommaTokenDoesNotResolve(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md,\n",
		"a.md":          "A-CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0 (a.md, does not exist)", inlined)
	}
	if content != "@a.md,\n" {
		t.Errorf("content = %q, want unchanged literal", content)
	}
}

func TestFlattenNoTrailingNewlineGetsOne(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n",
		"a.md":          "NO-TRAILING-NEWLINE",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	want := "Contents of a.md (project instructions, checked into the codebase):\n\nNO-TRAILING-NEWLINE\n\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestFlattenEmptyImportedFile(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n",
		"a.md":          "",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	want := "Contents of a.md (project instructions, checked into the codebase):\n\n\n\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

// TestFlattenGoldenSingleImport pins the EXACT full output for a
// nested/prose-surrounded tree (not just substring checks), matching what
// check.go's byte-exact comparison will require. Note the \n\n\n between
// content and Bottom: the expansion's ensured trailing newline plus the
// source's own blank line. This is a golden-output test pinning the exact
// rendering.
func TestFlattenGoldenSingleImport(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md":           "Top\n\n@PROJECT_MEMORY/index.md\n\nBottom\n",
		"PROJECT_MEMORY/index.md": "MEMORY CONTENT\n",
	})
	content, inlined, err := Flatten(optsFor(root))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	want := "Top\n" +
		"\n" +
		"Contents of PROJECT_MEMORY/index.md (project instructions, checked into the codebase):\n" +
		"\n" +
		"MEMORY CONTENT\n" +
		"\n" +
		"\n" +
		"Bottom\n"
	if content != want {
		t.Errorf("content = %q\nwant      %q", content, want)
	}
}

func TestFlattenAbsolutePathToken(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"outside/z.md": "ABS-CONTENT\n",
	})
	absTarget := filepath.Join(root, "outside", "z.md")
	srcDir := makeTree(t, map[string]string{}) // separate importer directory
	srcPath := filepath.Join(srcDir, "AGENTS.src.md")
	// "X " (with the space) keeps a preceding literal in play to prove it is
	// not eaten, while leaving the '@' at a valid token boundary — "X@" would
	// make the '@' mid-word and expand nothing (see atTokenBoundary).
	if err := os.WriteFile(srcPath, []byte("X @"+absTarget+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := optsFor(srcDir)
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	wantRel := filepath.ToSlash(mustRel(t, srcDir, absTarget))
	if !strings.Contains(content, "Contents of "+wantRel+" (project instructions, checked into the codebase):") {
		t.Errorf("content missing rootRel marker for %q\ngot:\n%s", wantRel, content)
	}
	if !strings.Contains(content, "ABS-CONTENT") {
		t.Errorf("content missing ABS-CONTENT\ngot:\n%s", content)
	}
	if !strings.HasPrefix(content, "X") {
		t.Errorf("adjacent literal 'X' prefix was eaten\ngot:\n%s", content)
	}
}

func mustRel(t *testing.T, base, target string) string {
	t.Helper()
	rel, err := filepath.Rel(base, target)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	return rel
}

func TestFlattenTokenOutsideRootDirUsesDotDot(t *testing.T) {
	t.Parallel()
	// RootDir is the "sub" directory; the import reaches a sibling directory
	// outside RootDir. The marker should show a "../" relative path — the
	// normal filepath.Rel behavior for a target outside the base directory,
	// with no special-casing needed.
	base := makeTree(t, map[string]string{
		"sub/AGENTS.src.md": "@../sibling/z.md\n",
		"sibling/z.md":      "SIBLING-CONTENT\n",
	})
	opts := Options{
		SrcPath:    filepath.Join(base, "sub", "AGENTS.src.md"),
		OutPath:    filepath.Join(base, "sub", "AGENTS.md"),
		RootDir:    filepath.Join(base, "sub"),
		MarkerDesc: DefaultMarkerDesc,
		SrcName:    "AGENTS.src.md",
		OutName:    "AGENTS.md",
	}
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	if !strings.Contains(content, "Contents of ../sibling/z.md (project instructions, checked into the codebase):") {
		t.Errorf("content missing ../ marker\ngot:\n%s", content)
	}
	if !strings.Contains(content, "SIBLING-CONTENT") {
		t.Errorf("content missing SIBLING-CONTENT\ngot:\n%s", content)
	}
}

// TestFlattenCustomMarkerDesc pins markerDesc()'s override branch: when
// Options.MarkerDesc is non-empty, it replaces DefaultMarkerDesc verbatim in
// every inline marker. optsFor deliberately leaves MarkerDesc unset so the
// rest of the suite exercises the default branch instead; this is the one
// test for the non-default path.
func TestFlattenCustomMarkerDesc(t *testing.T) {
	t.Parallel()
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n",
		"a.md":          "A-CONTENT\n",
	})
	opts := optsFor(root)
	opts.MarkerDesc = "custom marker text"
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	want := "Contents of a.md (custom marker text):\n\nA-CONTENT\n\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
	if strings.Contains(content, DefaultMarkerDesc) {
		t.Errorf("content should not contain the default marker desc\ngot:\n%s", content)
	}
}

func TestFlattenReadsFromStdinWhenSet(t *testing.T) {
	dir := t.TempDir()
	xPath := filepath.Join(dir, "x.md")
	if err := os.WriteFile(xPath, []byte("X-CONTENT\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := Options{
		SrcPath: filepath.Join(dir, "STDIN-SENTINEL-NOT-A-REAL-FILE.md"),
		RootDir: dir,
		Stdin:   strings.NewReader("Body\n\n@x.md\n"),
	}
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if inlined != 1 {
		t.Errorf("inlined = %d, want 1", inlined)
	}
	if !strings.Contains(content, "X-CONTENT") {
		t.Errorf("content = %q, want it to contain X-CONTENT", content)
	}
	if !strings.Contains(content, "Contents of x.md") {
		t.Errorf("content = %q, want an inline marker for x.md", content)
	}
}

func TestFlattenStdinEmptyIsNotAnError(t *testing.T) {
	opts := Options{
		SrcPath: filepath.Join(t.TempDir(), "unused.md"),
		RootDir: t.TempDir(),
		Stdin:   strings.NewReader(""),
	}
	content, inlined, err := Flatten(opts)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
	if inlined != 0 {
		t.Errorf("inlined = %d, want 0", inlined)
	}
}
