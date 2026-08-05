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

func optsFor(root string) Options {
	return Options{
		SrcPath:    filepath.Join(root, "AGENTS.src.md"),
		OutPath:    filepath.Join(root, "AGENTS.md"),
		RootDir:    root,
		MarkerDesc: DefaultMarkerDesc,
		SrcName:    "AGENTS.src.md",
		OutName:    "AGENTS.md",
	}
}

func TestFlattenInlinesSingleImport(t *testing.T) {
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

func TestFlattenDiamondEmitsSingleCopy(t *testing.T) {
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

func TestFlattenTerminatesOnCycle(t *testing.T) {
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
	root := makeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md\n", "a.md": "@b.md\n", "b.md": "@c.md\n", "c.md": "DEEP\n",
	})
	opts := optsFor(root)
	opts.MaxDepth, opts.MaxDepthSet = 2, true
	if _, _, err := Flatten(opts); err == nil {
		t.Fatal("Flatten: want depth error, got nil")
	} else if !strings.Contains(strings.ToLower(err.Error()), "depth") {
		t.Errorf("error %q should mention depth", err)
	}
}

func TestFlattenMaxDepthBoundaryAllowed(t *testing.T) {
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

func TestFlattenDirectoryTokenStaysLiteral(t *testing.T) {
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
	root := makeTree(t, map[string]string{"other.md": "x\n"})
	if _, _, err := Flatten(optsFor(root)); err == nil {
		t.Fatal("Flatten: want error for missing source, got nil")
	}
}

func TestFlattenSelfImportTerminates(t *testing.T) {
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

func TestFlattenAbsolutePathToken(t *testing.T) {
	root := makeTree(t, map[string]string{
		"outside/z.md": "ABS-CONTENT\n",
	})
	absTarget := filepath.Join(root, "outside", "z.md")
	srcDir := makeTree(t, map[string]string{}) // separate importer directory
	srcPath := filepath.Join(srcDir, "AGENTS.src.md")
	if err := os.WriteFile(srcPath, []byte("X@"+absTarget+"\n"), 0o644); err != nil {
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
	// RootDir is the "sub" directory; the import reaches a sibling directory
	// outside RootDir. The marker should show a "../" relative path, matching
	// Node's path.relative (no special-casing for outside-root paths).
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
