// Package test runs the at-include binary against data-only fixture cases.
//
// A case is a directory under `cases/`: `files/` is copied into a temp dir,
// `cmd` supplies argv, and `expect/` declares the exit code, output, and
// resulting files. Adding a case means adding a directory — no Go code.
// See test/README.md for the full case-directory format.
package test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// binary is built once for the whole suite by TestMain.
var binary string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// runMain does the TestMain work in its own function so that `defer` actually
// runs before we exit: os.Exit itself skips deferred calls, so TestMain must
// not call it directly around the cleanup.
func runMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "at-include-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "at-include")
	if isWindows() {
		binary += ".exe"
	}
	// Build directly with `go build` rather than shelling out to `mise run
	// build`: the mise task also injects a version string via -ldflags (git
	// describe), which we don't need here and which would make the suite
	// depend on git tag state. The one behavioral consequence is that
	// `--version` prints "dev" in this harness's binary instead of a real
	// version — see cases/version's cmd/expect for how that's handled.
	build := exec.Command("go", "build", "-o", binary, "../cmd/at-include")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		return 1
	}
	return m.Run()
}

func isWindows() bool { return os.PathSeparator == '\\' }

func TestCases(t *testing.T) {
	entries, err := os.ReadDir("cases")
	if err != nil {
		t.Fatalf("read cases dir: %v", err)
	}
	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no fixture cases found under cases/")
	}
	// The len(dirs)==0 check above must run, and possibly Fatal, before any
	// subtest is registered: t.Run with t.Parallel() subtests only *pause*
	// the parent goroutine at the point the parallel subtests are queued,
	// they don't skip statements written before the loop. Since the check is
	// textually before the loop that calls t.Run, it always executes (and can
	// always fail the suite) regardless of how the subtests below schedule
	// themselves.
	for _, e := range dirs {
		e := e
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel()
			runCase(t, filepath.Join("cases", e.Name()))
		})
	}
}

func runCase(t *testing.T, caseDir string) {
	t.Helper()

	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := copyTree(filepath.Join(caseDir, "files"), work); err != nil {
		t.Fatalf("copy fixture files: %v", err)
	}

	argv := readArgv(t, filepath.Join(caseDir, "cmd"))
	absBin, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("Abs(binary): %v", err)
	}

	cmd := exec.Command(absBin, argv...)
	cmd.Dir = work
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case runErr != nil:
		t.Fatalf("run %s %v: %v", absBin, argv, runErr)
	}

	expectDir := filepath.Join(caseDir, "expect")
	if want := readExpectedExit(t, filepath.Join(expectDir, "exit")); code != want {
		t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			code, want, stdout.String(), stderr.String())
	}
	compareStream(t, "stdout", filepath.Join(expectDir, "stdout"), stdout.String())
	compareStream(t, "stderr", filepath.Join(expectDir, "stderr"), stderr.String())
	compareFiles(t, filepath.Join(expectDir, "files"), work)
	checkAbsent(t, filepath.Join(expectDir, "absent"), work)
}

// readArgv reads one argv token per line, skipping blanks and # comments. A
// trailing newline at end of file (the common case: every editor adds one)
// must NOT produce a spurious empty final argv element, so blank lines are
// skipped rather than preserved as "" tokens. This means an argument that is
// genuinely the empty string cannot currently be expressed via `cmd` — no
// fixture case below needs one, so that limitation isn't documented further
// here; see test/README.md for the full format description.
func readArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read cmd: %v", err)
	}
	var argv []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		argv = append(argv, strings.TrimSpace(line))
	}
	return argv
}

func readExpectedExit(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read expect/exit: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("expect/exit is not a number: %v", err)
	}
	return n
}

// compareStream compares got against the expectation file.
//
// Trailing-newline policy: both sides are compared with exactly one trailing
// newline normalized away before the comparison (see trimOneTrailingNewline).
// This is deliberately NOT a byte-exact comparison, because the fixture files
// themselves always end in the editor-added trailing newline, and forcing
// every case author to author a `stdout` file with no final newline (or to
// remember some other special-case) is exactly the kind of foot-gun the
// review draft flagged. Stripping at most one trailing "\n" from each side:
//   - makes a genuinely-empty expected stdout (0-byte file) compare equal to
//     real empty stdout,
//   - makes a normal "Foo\n" fixture file compare equal to real "Foo\n"
//     stdout without the file needing to be byte-for-byte hand-crafted,
//   - still catches a *missing* trailing newline in program output (e.g. if
//     runGenerate's Fprintf lost its \n), because only ONE newline is
//     stripped, not all of them: "Foo" (no newline) and "Foo\n" remain
//     distinguishable.
//
// This is documented in test/README.md; keep the two in sync.
//
// When the file's first line is "~", each remaining non-empty line must
// appear somewhere in got (substring match per line) instead of an exact
// comparison — for messages like "Run: at-include ..." where matching the
// whole multi-line usage text exactly would be brittle.
func compareStream(t *testing.T, name, path, got string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read expect/%s: %v", name, err)
	}
	want := string(data)

	if rest, isSubstr := strings.CutPrefix(want, "~\n"); isSubstr {
		for _, line := range strings.Split(trimOneTrailingNewline(rest), "\n") {
			if line == "" {
				continue
			}
			if !strings.Contains(got, line) {
				t.Errorf("%s missing %q\ngot:\n%s", name, line, got)
			}
		}
		return
	}
	wantN := trimOneTrailingNewline(want)
	gotN := trimOneTrailingNewline(got)
	if gotN != wantN {
		t.Errorf("%s mismatch\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// trimOneTrailingNewline removes exactly one trailing "\n" (and a preceding
// "\r", if present) from s, or returns s unchanged if it doesn't end in one.
// Unlike strings.TrimRight(s, "\n"), this never collapses multiple trailing
// blank lines down to nothing, so a program that erroneously emits extra
// trailing blank lines is still caught.
func trimOneTrailingNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

// compareFiles walks expect/files and compares each one byte for byte against
// the same relative path in the work dir.
//
// CRLF is deliberately NOT normalized here: .gitattributes marks
// `test/cases/** -text`, which tells git to check fixture files out with
// their exact recorded bytes on every platform (no LF<->CRLF translation).
// Given that guarantee, a byte-exact comparison is strictly more useful than
// a CRLF-tolerant one: it also catches a real bug where the tool corrupts
// line endings (e.g. normalizes CRLF input to LF on the way through), which a
// normalizing comparison would silently hide. If a future case fixture
// legitimately needs to store CRLF content on purpose, it can — the
// -text attribute preserves it byte for byte either way.
func compareFiles(t *testing.T, expectFiles, work string) {
	t.Helper()
	if _, err := os.Stat(expectFiles); errors.Is(err, os.ErrNotExist) {
		return
	}
	err := filepath.WalkDir(expectFiles, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(expectFiles, path)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(work, rel))
		if err != nil {
			t.Errorf("expected file %s: %v", rel, err)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("file %s mismatch (byte-exact compare)\n--- want ---\n%s\n--- got ---\n%s", rel, want, got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk expect/files: %v", err)
	}
}

// checkAbsent reads expect/absent (one repo-relative-to-work path per line,
// blank lines and #-comments ignored) and fails the test if any listed path
// exists in work. This is the counterpart compareFiles is missing on its
// own: compareFiles only ever checks paths it's told to expect, so it can
// never notice an UNEXPECTED file the tool wrote (e.g. --list-imports or a
// usage error accidentally still producing an output file). expect/absent
// makes "this file must not exist" an explicit, positive assertion instead of
// something compareFiles's silence could be mistaken for.
func checkAbsent(t *testing.T, path, work string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read expect/absent: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join(work, trimmed)); err == nil {
			t.Errorf("expect/absent: %s exists but must not", trimmed)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expect/absent: stat %s: %v", trimmed, err)
		}
	}
}

func copyTree(src, dst string) error {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
