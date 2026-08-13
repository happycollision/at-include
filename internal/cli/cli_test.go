// Package cli tests intentionally do NOT call t.Parallel().
//
// Run's public signature is Run(argv []string, stdout, stderr io.Writer) int
// — it has no working-directory parameter, because relative
// --src/--out/--root paths must resolve against the process's actual CWD.
// Driving that from tests means calling os.Chdir, which mutates
// process-global state: two tests changing directory concurrently would race
// and could each observe the other's CWD. So every test in this file runs
// serially (the package's default, absent t.Parallel()), and `run` restores
// the previous CWD via defer before returning.
//
// A later task (the fixture suite under test/) execs the real compiled
// binary in its own temp directory per case, which exercises CWD-relative
// behavior end to end under real process isolation. These unit tests instead
// focus on flag parsing and wiring into the flatten package; they don't need
// to run in parallel with each other to do that.
package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
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

// run invokes Run with cwd set to dir and the given stdin content, returning
// code, stdout, stderr.
func run(t *testing.T, dir string, argv []string, stdin string) (code int, stdout, stderr string) {
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
	code = Run(argv, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunNoCommandIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, _, stderr := run(t, dir, nil, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "a command is required") {
		t.Fatalf("stderr = %q, want mention of required command", stderr)
	}
	if !strings.Contains(stderr, "Commands:") {
		t.Fatalf("stderr = %q, want top-level usage listing commands", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("bare invocation must not write AGENTS.md")
	}
}

func TestRunUnknownCommandIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, _, stderr := run(t, dir, []string{"bogus"}, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command: bogus") {
		t.Fatalf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestRunTopLevelHelpListsCommands(t *testing.T) {
	for _, argv := range [][]string{{"--help"}, {"-h"}} {
		code, stdout, _ := run(t, t.TempDir(), argv, "")
		if code != 0 {
			t.Fatalf("%v: exit = %d, want 0", argv, code)
		}
		for _, want := range []string{"Commands:", "build", "check", "supplement", "imports"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v: stdout missing %q", argv, want)
			}
		}
	}
}

func TestRunPerCommandHelp(t *testing.T) {
	for cmd, want := range map[string]string{
		"build":      "generated-file banner",
		"check":      "up to date",
		"supplement": "supplementary agent context",
		"imports":    "one per line",
	} {
		code, stdout, _ := run(t, t.TempDir(), []string{cmd, "--help"}, "")
		if code != 0 {
			t.Fatalf("%s --help: exit = %d, want 0", cmd, code)
		}
		if !strings.Contains(stdout, "at-include "+cmd) || !strings.Contains(stdout, want) {
			t.Fatalf("%s --help: stdout = %q, want command-specific usage", cmd, stdout)
		}
	}
}

func TestRunDefaultWritesOutput(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X-CLI\n"})
	code, stdout, stderr := run(t, dir, []string{"build"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	const want = "Generated AGENTS.md from AGENTS.src.md (1 files inlined).\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
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
	if code, _, se := run(t, dir, []string{"build"}, ""); code != 0 {
		t.Fatalf("generate failed: %d %s", code, se)
	}
	code, stdout, _ := run(t, dir, []string{"check"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0; stdout = %q", code, stdout)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRunCheckFailsWhenStale(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	if code, _, se := run(t, dir, []string{"build"}, ""); code != 0 {
		t.Fatalf("generate failed: %d %s", code, se)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.src.md"), []byte("CHANGED\n\n@x.md\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	code, stdout, _ := run(t, dir, []string{"check"}, "")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	want := "AGENTS.md is out of date. Run: at-include build\n" +
		"First difference around line 11:\n" +
		"  > in `AGENTS.src.md` and regenerate — do not edit this file.\n" +
		"  \n" +
		"- Body\n" +
		"+ CHANGED\n" +
		"  \n" +
		"  Contents of x.md (project instructions, checked into the codebase):\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunCheckMissingOutputFails(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, stdout, _ := run(t, dir, []string{"check"}, "")
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
	code, stdout, stderr := run(t, dir, []string{"check", "--max-depth", "0"}, "")
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
		code, stdout, _ := run(t, dir, []string{flag}, "")
		if code != 0 {
			t.Errorf("%s: code = %d, want 0", flag, code)
		}
		if !strings.Contains(stdout, "Usage") || !strings.Contains(stdout, "Commands:") {
			t.Errorf("%s: stdout = %q", flag, stdout)
		}
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	code, _, stderr := run(t, dir, []string{"--bogus"}, "")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command: --bogus") || !strings.Contains(stderr, "Commands:") {
		t.Errorf("stderr = %q", stderr)
	}

	code, _, stderr = run(t, dir, []string{"build", "--bogus"}, "")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown flag") || !strings.Contains(stderr, "Usage") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunPerCommandFlagAllowlist(t *testing.T) {
	for _, tc := range []struct{ argv []string }{
		{[]string{"imports", "--out", "x.md"}},
		{[]string{"imports", "--max-depth", "2"}},
		{[]string{"imports", "--marker-desc", "s"}},
		{[]string{"imports", "--root", "."}},
		{[]string{"supplement", "--check"}},
		{[]string{"build", "--hook-mode"}},
		{[]string{"build", "--list-imports"}},
		{[]string{"build", "--version"}},
		{[]string{"build", "--bogus"}},
	} {
		code, _, stderr := run(t, t.TempDir(), tc.argv, "")
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2 (stderr: %s)", tc.argv, code, stderr)
		}
		if !strings.Contains(stderr, "unknown flag") {
			t.Fatalf("%v: stderr = %q, want unknown-flag message", tc.argv, stderr)
		}
	}
}

func TestRunMaxDepth(t *testing.T) {
	files := map[string]string{"AGENTS.src.md": "@a.md\n", "a.md": "@b.md\n", "b.md": "DEEP\n"}

	dir := writeTree(t, files)
	if code, _, se := run(t, dir, []string{"build", "--max-depth", "2"}, ""); code != 0 {
		t.Errorf("maxDepth 2: code = %d, want 0; stderr = %q", code, se)
	}

	dir2 := writeTree(t, files)
	code, _, stderr := run(t, dir2, []string{"build", "--max-depth", "1"}, "")
	if code != 1 {
		t.Errorf("maxDepth 1: code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "at-include:") || !strings.Contains(stderr, "depth") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestParseArgsMaxDepthGrammar pins the exact grammar --max-depth accepts: a
// plain non-negative integer via strconv.Atoi, nothing more — no leading "+",
// no "0x"/"0o"/"0b" prefixes, no scientific notation, no decimals. "007" is
// valid because strconv.Atoi treats a leading zero as plain decimal, not
// octal.
func TestParseArgsMaxDepthGrammar(t *testing.T) {
	valid := []struct {
		raw  string
		want int
	}{
		{"0", 0},
		{"2", 2},
		{"007", 7},
	}
	for _, tc := range valid {
		o, err := parseFlags(commands["build"], []string{"--max-depth", tc.raw})
		if err != nil {
			t.Errorf("parseFlags(--max-depth %q): unexpected error %v", tc.raw, err)
			continue
		}
		if !o.maxDepthSet || o.maxDepth != tc.want {
			t.Errorf("parseFlags(--max-depth %q): maxDepth = %d, maxDepthSet = %v, want %d, true",
				tc.raw, o.maxDepth, o.maxDepthSet, tc.want)
		}
	}

	invalid := []string{"", "-1", "1.5", "notanumber", "1e2", "0x10", "1_000"}
	for _, raw := range invalid {
		if _, err := parseFlags(commands["build"], []string{"--max-depth", raw}); err == nil {
			t.Errorf("parseFlags(--max-depth %q): want error, got nil", raw)
		}
	}
}

func TestParseArgsSrcSetTracksExplicitFlag(t *testing.T) {
	o, err := parseFlags(commands["build"], []string{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if o.srcSet {
		t.Error("want srcSet = false when --src is not passed")
	}

	o, err = parseFlags(commands["build"], []string{"--src", "custom.md"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !o.srcSet {
		t.Error("want srcSet = true when --src is passed")
	}
	if o.src != "custom.md" {
		t.Errorf("src = %q, want %q", o.src, "custom.md")
	}
}

func TestRunMaxDepthInvalidValues(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	// "1e2", "0x10", and "1_000" are deliberately included here: --max-depth
	// only ever needs a plain non-negative integer, and strconv.Atoi rejects
	// all three forms.
	for _, v := range []string{"notanumber", "", "-1", "1.5", "1e2", "0x10", "1_000"} {
		code, _, stderr := run(t, dir, []string{"build", "--max-depth", v}, "")
		if code != 2 {
			t.Errorf("--max-depth %q: code = %d, want 2", v, code)
		}
		if !strings.Contains(stderr, "non-negative integer") {
			t.Errorf("--max-depth %q: stderr = %q", v, stderr)
		}
	}
	code, _, stderr := run(t, dir, []string{"build", "--max-depth"}, "")
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
		[]string{"build", "--src", "docs/CLAUDE.src.md", "--out", "docs/CLAUDE.md", "--root", "."}, "")
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
	code, _, stderr := run(t, dir, []string{"build"}, "")
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
	code, stdout, stderr := run(t, dir, []string{"imports"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got, want := stdout, "a.md\nc.md\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("imports must not write the output file")
	}
}

func TestRunImportsSrcIsDirectoryIsRuntimeError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, _, stderr := run(t, dir, []string{"imports", "--src", "."}, "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (runtime error, not a usage error)", code)
	}
	if !strings.Contains(stderr, "at-include:") {
		t.Fatalf("stderr = %q, want an at-include: error line", stderr)
	}
	if strings.Contains(stderr, "--out") {
		t.Fatalf("stderr = %q must not blame --out; imports does not take --out", stderr)
	}
}

func TestRunVersion(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	code, stdout, _ := run(t, dir, []string{"--version"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("--version should print something")
	}
}

func TestRunMarkerDescOverride(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "@x.md\n", "x.md": "X\n"})
	if code, _, se := run(t, dir, []string{"build", "--marker-desc", "custom wording"}, ""); code != 0 {
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

// --- Fix 1: --out must never resolve to the same file as --src ---
//
// This is the DATA LOSS bug: `at-include --out AGENTS.src.md` used to happily
// overwrite the hand-authored source with the generated banner + (now
// self-referential) content, and reported success (exit 0). Every case below
// asserts BOTH the exit code/stderr AND that the source file's bytes are
// completely untouched by the rejected run — a nonzero exit code alone would
// not catch a "partial write before erroring" regression.

func TestRunOutSameAsSrcIsRejected(t *testing.T) {
	const original = "AUTHORED SOURCE\n\n@x.md\n"
	dir := writeTree(t, map[string]string{"AGENTS.src.md": original, "x.md": "X\n"})

	code, stdout, stderr := run(t, dir, []string{"build", "--out", "AGENTS.src.md"}, "")
	if code != 2 {
		t.Errorf("code = %d, want 2 (usage error)", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty on this usage error, got %q", stdout)
	}
	if !strings.Contains(stderr, "--out") || !strings.Contains(stderr, "--src") {
		t.Errorf("stderr should name both flags, got %q", stderr)
	}
	if !strings.Contains(stderr, "Usage") {
		t.Errorf("stderr should include usage text like other usage errors, got %q", stderr)
	}

	after, err := os.ReadFile(filepath.Join(dir, "AGENTS.src.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != original {
		t.Errorf("source file was modified by the rejected run: got %q, want %q", after, original)
	}
}

func TestRunOutSameAsSrcDifferentSpellingIsRejected(t *testing.T) {
	const original = "AUTHORED SOURCE\n\n@x.md\n"
	dir := writeTree(t, map[string]string{"AGENTS.src.md": original, "x.md": "X\n"})

	// "./AGENTS.src.md" resolves to the same absolute path as the default
	// --src "AGENTS.src.md", via a different spelling than a bare Clean
	// no-op comparison of the raw strings would catch trivially — this
	// exercises the filepath.Abs + filepath.Clean normalization, not just an
	// exact string match.
	code, _, stderr := run(t, dir, []string{"build", "--out", "./AGENTS.src.md"}, "")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--out") {
		t.Errorf("stderr = %q", stderr)
	}

	after, err := os.ReadFile(filepath.Join(dir, "AGENTS.src.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != original {
		t.Errorf("source file was modified by the rejected run: got %q, want %q", after, original)
	}
}

func TestRunOutSymlinkToSrcIsRejected(t *testing.T) {
	const original = "AUTHORED SOURCE\n\n@x.md\n"
	dir := writeTree(t, map[string]string{"AGENTS.src.md": original, "x.md": "X\n"})
	if err := os.Symlink(filepath.Join(dir, "AGENTS.src.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	code, _, stderr := run(t, dir, []string{"build", "--out", "link.md"}, "")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--out") {
		t.Errorf("stderr = %q", stderr)
	}

	after, err := os.ReadFile(filepath.Join(dir, "AGENTS.src.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != original {
		t.Errorf("source file was modified by the rejected run: got %q, want %q", after, original)
	}
}

func TestRunOutDifferentFromSrcStillWorks(t *testing.T) {
	// Sanity check alongside the rejection tests above: an --out that merely
	// happens to share a directory (or a name prefix) with --src, but is not
	// actually the same file, must still generate normally.
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	code, _, stderr := run(t, dir, []string{"build", "--out", "AGENTS.src.md.generated"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.src.md.generated")); err != nil {
		t.Errorf("expected output file to be written: %v", err)
	}
}

// --- Fix 2: regenCommand must print a command that, run from the caller's
// CWD, actually regenerates the right files and makes --check pass. ---

func TestRegenCommandDefaults(t *testing.T) {
	got := regenCommand(options{src: defaultSrc, out: defaultOut})
	want := "at-include build"
	if got != want {
		t.Errorf("regenCommand = %q, want %q", got, want)
	}
}

func TestRegenCommandSubdirectorySrc(t *testing.T) {
	// This is the bug as reported: --src/--out under docs/ must appear in the
	// suggestion exactly as the user typed them (CWD-relative), not collapsed
	// to root-relative display names that happen to omit the docs/ prefix.
	got := regenCommand(options{
		src: "docs/CLAUDE.src.md",
		out: "docs/CLAUDE.md",
	})
	want := "at-include build --src docs/CLAUDE.src.md --out docs/CLAUDE.md"
	if got != want {
		t.Errorf("regenCommand = %q, want %q", got, want)
	}
}

func TestRegenCommandEmitsRoot(t *testing.T) {
	// With --root explicitly set (even to "."), the suggestion must include
	// it: omitting --root here means the regenerated markers resolve against
	// a different root than the one --check actually used, so following the
	// printed command would NOT make --check pass.
	got := regenCommand(options{
		src: defaultSrc, out: defaultOut,
		root: ".", rootSet: true,
	})
	want := "at-include build --root ."
	if got != want {
		t.Errorf("regenCommand = %q, want %q", got, want)
	}
}

func TestRegenCommandAllFlags(t *testing.T) {
	got := regenCommand(options{
		src: "docs/CLAUDE.src.md", out: "docs/CLAUDE.md",
		root: "docs", rootSet: true,
		maxDepth: 5, maxDepthSet: true,
		markerDesc: "custom wording", markerDescSet: true,
	})
	want := "at-include build --src docs/CLAUDE.src.md --out docs/CLAUDE.md --root docs --max-depth 5 --marker-desc 'custom wording'"
	if got != want {
		t.Errorf("regenCommand = %q, want %q", got, want)
	}
}

func TestRegenCommandExplicitEmptyMarkerDesc(t *testing.T) {
	// An explicitly-passed `--marker-desc ""` must still be echoed (as `''`),
	// distinguishing it from "not passed" — this is what markerDescSet is for.
	got := regenCommand(options{
		src: defaultSrc, out: defaultOut,
		markerDesc: "", markerDescSet: true,
	})
	want := "at-include build --marker-desc ''"
	if got != want {
		t.Errorf("regenCommand = %q, want %q", got, want)
	}
}

// TestRunCheckSuggestionRoundTrips is the end-to-end guarantee behind Fix 2:
// generate into a subdirectory with an explicit --root, go stale, run
// --check, and confirm that running the EXACT command printed after "Run: "
// (parsed the same way argv normally would be) makes a follow-up --check
// pass. This is what "the suggestion actually works" means operationally.
func TestRunCheckSuggestionRoundTrips(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"docs/CLAUDE.src.md": "Body\n\n@notes.md\n",
		"docs/notes.md":      "NOTES\n",
	})
	argv := []string{"build", "--src", "docs/CLAUDE.src.md", "--out", "docs/CLAUDE.md", "--root", "."}
	if code, _, se := run(t, dir, argv, ""); code != 0 {
		t.Fatalf("initial generate failed: %d %s", code, se)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "CLAUDE.src.md"), []byte("CHANGED\n\n@notes.md\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, stdout, stderr := run(t, dir, []string{"check", "--src", "docs/CLAUDE.src.md", "--out", "docs/CLAUDE.md", "--root", "."}, "")
	if code != 1 {
		t.Fatalf("--check should fail while stale: code = %d, stderr = %q", code, stderr)
	}
	const prefix = "docs/CLAUDE.md is out of date. Run: "
	if !strings.HasPrefix(stdout, prefix) {
		t.Fatalf("stdout = %q, want prefix %q", stdout, prefix)
	}
	rest := strings.TrimPrefix(stdout, prefix)
	suggestedLine, _, _ := strings.Cut(rest, "\n")
	wantSuggestion := "at-include build --src docs/CLAUDE.src.md --out docs/CLAUDE.md --root ."
	if suggestedLine != wantSuggestion {
		t.Fatalf("suggested command = %q, want %q", suggestedLine, wantSuggestion)
	}

	suggestedArgv := strings.Fields(suggestedLine)[1:] // drop the leading "at-include"
	if code, _, se := run(t, dir, suggestedArgv, ""); code != 0 {
		t.Fatalf("following the suggested command failed: %d %s", code, se)
	}

	// The suggested command's flags (minus the leading "build") plus "check"
	// must now report up to date.
	checkArgv := append([]string{"check"}, suggestedArgv[1:]...)
	code, stdout, stderr = run(t, dir, checkArgv, "")
	if code != 0 {
		t.Fatalf("--check after following the suggestion should pass: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("stdout = %q", stdout)
	}
}

// --- Fix 4: shellQuote must produce output that is actually safe to paste
// into a POSIX shell, not just visually plausible. ---

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"has spaces", "'has spaces'"},
		{`has "quotes" and $dollar`, `'has "quotes" and $dollar'`},
		{"has'single'quotes", `'has'\''single'\''quotes'`},
		{"has`backtick`", "'has`backtick`'"},
		{"has!bang", "'has!bang'"},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveOptionsSrcDashSetsStdinModeAndCwdRoot(t *testing.T) {
	dir := writeTree(t, map[string]string{"unrelated.md": "x\n"})
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	o, err := parseFlags(commands["build"], []string{"--src", "-"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	fOpts, err := resolveOptions(o)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	// resolveOptions itself no longer sets fOpts.Stdin (Run does, since it's
	// the one holding the real io.Reader); resolveOptions signals stdin mode
	// via SrcName/SrcPath instead.
	if fOpts.SrcName != "-" {
		t.Errorf("SrcName = %q, want %q (stdin mode when --src is -)", fOpts.SrcName, "-")
	}
	if fOpts.RootDir != dir {
		t.Errorf("RootDir = %q, want %q (CWD default when --src is -)", fOpts.RootDir, dir)
	}
}

func TestResolveOptionsOutDashMeansStdout(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	o, err := parseFlags(commands["build"], []string{"--out", "-"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	fOpts, err := resolveOptions(o)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	if !fOpts.OutIsStdout {
		t.Error("OutIsStdout should be true when --out is -")
	}
}

func TestResolveOptionsOutDashSetsOutNameToDash(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "x\n"})
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	o, err := parseFlags(commands["build"], []string{"--out", "-"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	fOpts, err := resolveOptions(o)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	if fOpts.OutName != "-" {
		t.Errorf("OutName = %q, want %q", fOpts.OutName, "-")
	}
}

func TestResolveOptionsSrcDashDefaultsOutToStdout(t *testing.T) {
	dir := writeTree(t, map[string]string{"unrelated.md": "x\n"})
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	o, err := parseFlags(commands["build"], []string{"--src", "-"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	fOpts, err := resolveOptions(o)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	if !fOpts.OutIsStdout {
		t.Error("OutIsStdout should default to true when --src is - and --out wasn't explicitly given")
	}
}

func TestResolveOptionsSrcDashSkipsCollisionCheck(t *testing.T) {
	dir := writeTree(t, map[string]string{"unrelated.md": "x\n"})
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	// "-" as both --src and --out must never trigger the same-path usage
	// error: they're independent streams, not the same file.
	o, err := parseFlags(commands["build"], []string{"--src", "-", "--out", "-"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if _, err := resolveOptions(o); err != nil {
		t.Errorf("resolveOptions(--src - --out -): unexpected error %v", err)
	}
}

// TestShellQuoteRoundTripsThroughShell is the load-bearing assertion behind
// Fix 4: actually run the quoted output through /bin/sh and confirm the value
// comes back out byte-for-byte, for values containing every metacharacter the
// old strconv.Quote-based quoteArg mishandled (in particular "$", which
// double-quoting leaves live for shell expansion).
func TestShellQuoteRoundTripsThroughShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	cases := []string{
		`has "quotes" and $dollar`,
		"has'single'quotes",
		"has`backtick`and$var",
		"has!bang and $HOME",
		"plain",
		"",
		`mix of ' and " and $ and ` + "`",
	}
	for _, in := range cases {
		quoted := shellQuote(in)
		cmd := exec.Command("sh", "-c", "printf '%s' "+quoted)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh -c failed for input %q (quoted %q): %v", in, quoted, err)
		}
		if string(out) != in {
			t.Errorf("round-trip through sh: input %q, quoted %q, got back %q", in, quoted, string(out))
		}
	}
}

func TestRunSrcDashReadsStdinAndWritesStdout(t *testing.T) {
	dir := writeTree(t, map[string]string{"x.md": "X-CONTENT\n"})
	code, stdout, stderr := run(t, dir, []string{"build", "--src", "-"}, "Body\n\n@x.md\n")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "X-CONTENT") {
		t.Errorf("stdout = %q, want it to contain X-CONTENT", stdout)
	}
	if strings.Contains(stdout, "[!IMPORTANT]") {
		t.Errorf("stdout should not contain the generated-file banner: %q", stdout)
	}
	if strings.Contains(stdout, "Generated") {
		t.Errorf("stdout should not contain the success message when --out is stdout: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("--src - with no --out must not write AGENTS.md")
	}
}

func TestRunSrcDashEmptyStdinIsNotAnError(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	code, stdout, stderr := run(t, dir, []string{"build", "--src", "-"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if stdout != "\n" && stdout != "" {
		t.Errorf("stdout = %q, want empty (or a single trailing newline)", stdout)
	}
}

func TestRunSrcDashOutToRealFileSkipsBanner(t *testing.T) {
	dir := writeTree(t, map[string]string{"x.md": "X\n"})
	code, _, stderr := run(t, dir, []string{"build", "--src", "-", "--out", "result.md"}, "@x.md\n")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	written, err := os.ReadFile(filepath.Join(dir, "result.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(written), "[!IMPORTANT]") {
		t.Errorf("output file should not contain the banner when --src is -: %s", written)
	}
	if !strings.Contains(string(written), "X\n") {
		t.Errorf("output file should contain flattened content: %s", written)
	}
}

func TestRunSrcRealOutDashStillShowsBanner(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n\n@x.md\n", "x.md": "X\n"})
	code, stdout, stderr := run(t, dir, []string{"build", "--out", "-"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "[!IMPORTANT]") {
		t.Errorf("stdout should contain the banner when --src is a real file: %q", stdout)
	}
	if strings.Contains(stdout, "Generated") {
		t.Errorf("stdout should not contain the success message when --out is stdout: %q", stdout)
	}
	if !strings.Contains(stdout, "--out -") {
		t.Errorf("banner's regenerate command should mention --out - (not a wrong AGENTS.md fallback): %q", stdout)
	}
}

func TestRunCheckWithSrcDashIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	code, _, stderr := run(t, dir, []string{"check", "--src", "-"}, "Body\n")
	if code != 2 {
		t.Errorf("code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "check") {
		t.Errorf("stderr should mention check, got %q", stderr)
	}
	if !strings.Contains(stderr, "--src -") && !strings.Contains(stderr, "stdin") {
		t.Errorf("stderr should explain check can't be combined with stdin source, got %q", stderr)
	}
}

func TestRunCheckWithOutDashIsUsageError(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.src.md": "Body\n"})
	code, _, stderr := run(t, dir, []string{"check", "--out", "-"}, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "check does not accept --out -") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunListImportsSrcDash(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	code, stdout, stderr := run(t, dir, []string{"imports", "--src", "-"}, "@a.md and `@b.md` and @c.md\n")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got, want := stdout, "a.md\nc.md\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunSupplementDefaultsSrcToAgentsAndOutToStdout(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"AGENTS.md": "Body\n\n@x.md\n",
		"x.md":      "X-HOOK\n",
	})
	code, stdout, stderr := run(t, dir, []string{"supplement"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "X-HOOK") {
		t.Errorf("stdout should contain expanded content, got %q", stdout)
	}
	if !strings.Contains(stdout, "pre-expanded") {
		t.Errorf("stdout should contain the supplement preamble, got %q", stdout)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if want := []string{"AGENTS.md", "x.md"}; !slices.Equal(names, want) {
		t.Errorf("supplement must not write any new file, dir contains %v, want %v", names, want)
	}
	agentsContent, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(agentsContent) != "Body\n\n@x.md\n" {
		t.Errorf("AGENTS.md should be unmodified by supplement, got %q", agentsContent)
	}
}

func TestRunSupplementWithStdinSourceStillGetsPreamble(t *testing.T) {
	dir := writeTree(t, map[string]string{"x.md": "X-STDIN\n"})
	code, stdout, stderr := run(t, dir, []string{"supplement", "--src", "-"}, "Body\n\n@x.md\n")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "pre-expanded") {
		t.Errorf("stdout should contain the supplement preamble even with stdin source, got %q", stdout)
	}
	if !strings.Contains(stdout, "X-STDIN") {
		t.Errorf("stdout should contain expanded stdin content, got %q", stdout)
	}
}

func TestRunSupplementExplicitOutStillWritesFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"AGENTS.md": "Body\n\n@x.md\n",
		"x.md":      "X-HOOK\n",
	})
	code, stdout, stderr := run(t, dir, []string{"supplement", "--out", "hook-context.md"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("explicit file --out should not also print to stdout, got %q", stdout)
	}
	written, err := os.ReadFile(filepath.Join(dir, "hook-context.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(written), "X-HOOK") || !strings.Contains(string(written), "pre-expanded") {
		t.Errorf("written file looks wrong:\n%s", written)
	}
}

func TestRunSupplementExplicitSrcOverridesDefault(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"custom.md": "Body\n\n@x.md\n",
		"x.md":      "X-CUSTOM\n",
	})
	code, stdout, stderr := run(t, dir, []string{"supplement", "--src", "custom.md"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "X-CUSTOM") {
		t.Errorf("stdout should contain custom.md's expansion, got %q", stdout)
	}
}

func TestRunSupplementMissingDefaultSourceIsSilent(t *testing.T) {
	dir := writeTree(t, map[string]string{}) // no AGENTS.md at all
	code, stdout, stderr := run(t, dir, []string{"supplement"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr should be empty, got %q", stderr)
	}
}

func TestRunSupplementMissingExplicitSourceIsAlsoSilent(t *testing.T) {
	dir := writeTree(t, map[string]string{}) // custom.md does not exist either
	code, stdout, stderr := run(t, dir, []string{"supplement", "--src", "custom.md"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr should be empty, got %q", stderr)
	}
}

func TestRunSupplementSourceExistsButNoImportsStillPrintsPreamble(t *testing.T) {
	dir := writeTree(t, map[string]string{"AGENTS.md": "Just plain body text, no imports.\n"})
	code, stdout, stderr := run(t, dir, []string{"supplement"}, "")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "pre-expanded") {
		t.Errorf("preamble should still print even with zero imports, got %q", stdout)
	}
}

func TestRunSupplementMissingSourceTruncatesExplicitOutFile(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	outPath := filepath.Join(dir, "ctx.md")
	if err := os.WriteFile(outPath, []byte("STALE PREVIOUS CONTENT\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	code, stdout, stderr := run(t, dir, []string{"supplement", "--out", "ctx.md"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr should be empty, got %q", stderr)
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("ctx.md should be truncated to empty, got %q", written)
	}
}

func TestRunSupplementMissingSourceWithStdoutOutDoesNotTouchDisk(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	code, stdout, stderr := run(t, dir, []string{"supplement"}, "")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("expected silent, got stdout=%q stderr=%q", stdout, stderr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("no files should be written when --out defaults to stdout, dir has: %v", entries)
	}
}
