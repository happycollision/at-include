// Package cli tests intentionally do NOT call t.Parallel().
//
// Run's public signature (fixed by the design spec) is
// Run(argv []string, stdout, stderr io.Writer) int — it has no working-
// directory parameter, because relative --src/--out/--root paths must resolve
// against the process's actual CWD, mirroring the JS's implicit
// process.cwd()-relative resolution. Driving that from tests means calling
// os.Chdir, which mutates process-global state: two tests changing directory
// concurrently would race and could each observe the other's CWD. So every
// test in this file runs serially (the package's default, absent
// t.Parallel()), and `run` restores the previous CWD via defer before
// returning.
//
// A later task (the fixture suite under test/) execs the real compiled
// binary in its own temp directory per case, which exercises CWD-relative
// behavior end to end under real process isolation. These unit tests instead
// focus on flag parsing and wiring into the flatten package; they don't need
// to run in parallel with each other to do that.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files under a fresh temp dir and returns its path.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
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

// run invokes Run with cwd set to dir, returning code, stdout, stderr.
func run(t *testing.T, dir string, argv ...string) (int, string, string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	var out, errOut bytes.Buffer
	code := Run(argv, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunDefaultWritesOutput(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X-CLI\n"})
	code, stdout, stderr := run(t, dir)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Generated AGENTS.md") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "1 files inlined") {
		t.Errorf("stdout should report the inlined count, got %q", stdout)
	}
	written, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(written), "> [!IMPORTANT]") || !strings.Contains(string(written), "X-CLI") {
		t.Errorf("written output looks wrong:\n%s", written)
	}
}

func TestRunCheckPassesAfterGenerate(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	if code, _, se := run(t, dir); code != 0 {
		t.Fatalf("generate failed: %d %s", code, se)
	}
	code, stdout, _ := run(t, dir, "--check")
	if code != 0 {
		t.Errorf("code = %d, want 0; stdout = %q", code, stdout)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRunCheckFailsWhenStale(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	if code, _, se := run(t, dir); code != 0 {
		t.Fatalf("generate failed: %d %s", code, se)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.src.md"), []byte("CHANGED\n\n@x.md\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	code, stdout, _ := run(t, dir, "--check")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(strings.ToLower(stdout), "out of date") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "First difference") {
		t.Errorf("stdout should include the diff excerpt, got %q", stdout)
	}
}

func TestRunCheckMissingOutputFails(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, stdout, _ := run(t, dir, "--check")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "out of date") {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestRunCheckRuntimeErrorGoesToStderr exercises the branch where
// flatten.Check itself returns an error (as opposed to reporting a merely
// stale/missing output) — here, a resolved import chain exceeding
// --max-depth. This must land on stderr, matching the "runtime error" row of
// the CLI's behavior contract, not stdout.
func TestRunCheckRuntimeErrorGoesToStderr(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "@a.md\n", "a.md": "DEEP\n"})
	code, stdout, stderr := run(t, dir, "--check", "--max-depth", "0")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty on a runtime error, got %q", stdout)
	}
	if !strings.Contains(stderr, "at-include:") || !strings.Contains(stderr, "depth") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunHelp(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	for _, flag := range []string{"--help", "-h"} {
		code, stdout, _ := run(t, dir, flag)
		if code != 0 {
			t.Errorf("%s: code = %d, want 0", flag, code)
		}
		if !strings.Contains(stdout, "Usage") {
			t.Errorf("%s: stdout = %q", flag, stdout)
		}
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	code, _, stderr := run(t, dir, "--bogus")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Unknown argument") || !strings.Contains(stderr, "Usage") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunMaxDepth(t *testing.T) {
	files := map[string]string{"AGENTS.src.md": "@a.md\n", "a.md": "@b.md\n", "b.md": "DEEP\n"}

	dir := writeTree(t, files)
	if code, _, se := run(t, dir, "--max-depth", "2"); code != 0 {
		t.Errorf("maxDepth 2: code = %d, want 0; stderr = %q", code, se)
	}

	dir2 := writeTree(t, files)
	code, _, stderr := run(t, dir2, "--max-depth", "1")
	if code != 1 {
		t.Errorf("maxDepth 1: code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "at-include:") || !strings.Contains(stderr, "depth") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunMaxDepthInvalidValues(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	for _, v := range []string{"notanumber", "", "-1", "1.5"} {
		code, _, stderr := run(t, dir, "--max-depth", v)
		if code != 2 {
			t.Errorf("--max-depth %q: code = %d, want 2", v, code)
		}
		if !strings.Contains(stderr, "non-negative integer") {
			t.Errorf("--max-depth %q: stderr = %q", v, stderr)
		}
	}
	code, _, stderr := run(t, dir, "--max-depth")
	if code != 2 {
		t.Errorf("--max-depth with no value: code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "non-negative integer") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunCustomSrcOutAndRoot(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"docs/CLAUDE.src.md": "Body\n\n@notes.md\n",
		"docs/notes.md":      "NOTES\n",
	})
	code, stdout, stderr := run(t, dir,
		"--src", "docs/CLAUDE.src.md", "--out", "docs/CLAUDE.md", "--root", ".")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Generated docs/CLAUDE.md") {
		t.Errorf("stdout = %q", stdout)
	}
	written, err := os.ReadFile(filepath.Join(dir, "docs", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(written), "Contents of docs/notes.md") {
		t.Errorf("marker should be root-relative:\n%s", written)
	}
	if !strings.Contains(string(written), "CLAUDE.src.md") {
		t.Errorf("banner should name the configured source:\n%s", written)
	}
}

func TestRunMissingSourceIsRuntimeError(t *testing.T) {
	dir := writeTree(t, map[string]string{"other.md": "x\n"})
	code, _, stderr := run(t, dir)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "at-include:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunListImports(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"AGENTS.src.md": "@a.md and `@b.md` and @c.md\n",
	})
	code, stdout, stderr := run(t, dir, "--list-imports")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got, want := stdout, "a.md\nc.md\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("--list-imports must not write the output file")
	}
}

func TestRunVersion(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	code, stdout, _ := run(t, dir, "--version")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("--version should print something")
	}
}

func TestRunMarkerDescOverride(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "@x.md\n", "x.md": "X\n"})
	if code, _, se := run(t, dir, "--marker-desc", "custom wording"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, se)
	}
	written, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(written), "Contents of x.md (custom wording):") {
		t.Errorf("marker desc not applied:\n%s", written)
	}
}
