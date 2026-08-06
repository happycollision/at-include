// Package cli implements the at-include command line: argument parsing and
// the generate/--check/--list-imports/--version/--help behaviors described in
// the design spec. It is a thin wrapper over internal/flatten — all of the
// actual import-flattening logic lives there.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/happycollision/at-include/internal/flatten"
)

// Version is overridden at build time by linking with:
//
//	-ldflags "-X github.com/happycollision/at-include/internal/cli.Version=v1.2.3"
//
// This is the ONE injection point: mise.toml's build task uses this exact
// -X path, and Task 8's goreleaser config must use the same path (there is
// no second copy of the version string anywhere else in the program — in
// particular, cmd/at-include/main.go does NOT declare its own `version`
// variable to copy into this one, precisely so there is only one place that
// can go stale relative to the other).
var Version = "dev"

const (
	defaultSrc = "AGENTS.src.md"
	defaultOut = "AGENTS.md"
)

const usage = `Usage: at-include [options]

Flatten @<path> Markdown imports, the way Claude Code inlines files referenced
from a CLAUDE.md. Reads a source file, replaces every @<path> that resolves to a
real file with that file's contents (recursively), and writes the result.

Options:
  (no args)           Generate and write the output file
  --check             Verify the output file is up to date; exit nonzero if not
  --src <path>        Source file (default: AGENTS.src.md)
  --out <path>        Output file (default: AGENTS.md)
  --root <path>       Root for marker paths (default: the source file's directory)
  --max-depth <n>     Error if a resolved import chain exceeds n hops
  --marker-desc <s>   Override the text in "Contents of X (<s>):"
  --list-imports      Print the @path candidates found in the source, one per line
  --version           Print the version
  --help, -h          Show this help

Exit codes: 0 success, 1 out-of-date or runtime error, 2 usage error.
`

// options holds the parsed command line, before paths are resolved against a
// working directory.
type options struct {
	check         bool
	help          bool
	version       bool
	listImports   bool
	src           string
	out           string
	outSet        bool
	root          string
	rootSet       bool
	markerDesc    string
	markerDescSet bool
	maxDepth      int
	maxDepthSet   bool
}

// usageError marks a resolveOptions failure as a usage error (bad flag
// combination, detectable before doing any work) rather than a runtime error
// (something that went wrong while acting on otherwise-valid flags). Run maps
// this to exit code 2, matching the same code parseArgs failures already use,
// since both are "the invocation itself is wrong" rather than "the invocation
// was fine but something failed while executing it".
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// Run parses argv (excluding the program name), performs the requested
// action, and returns the process exit code. Relative --src/--out/--root
// values resolve against the process's current working directory, so the
// same command behaves the same way regardless of where it's invoked from
// within a repo. stdin supplies the source text when `--src -` is given; it
// is otherwise unused.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := parseArgs(argv)
	if err != nil {
		fprintf(stderr, "%s\n\n%s", err, usage)
		return 2
	}
	if opts.help {
		fprint(stdout, usage)
		return 0
	}
	if opts.version {
		fprintf(stdout, "at-include %s\n", Version)
		return 0
	}

	fOpts, err := resolveOptions(opts)
	if err != nil {
		var uerr *usageError
		if errors.As(err, &uerr) {
			fprintf(stderr, "at-include: %s\n\n%s", err, usage)
			return 2
		}
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	if opts.src == "-" {
		fOpts.Stdin = stdin
	}

	if opts.check && opts.src == "-" {
		fprintf(stderr, "at-include: --check cannot be combined with --src - "+
			"(stdin content is transient and can't be compared against on a later run)\n\n%s", usage)
		return 2
	}

	switch {
	case opts.listImports:
		return runListImports(fOpts, stdout, stderr)
	case opts.check:
		return runCheck(opts, fOpts, stdout, stderr)
	default:
		return runGenerate(fOpts, stdout, stderr)
	}
}

// resolveOptions turns CLI strings into absolute paths and display names.
//
// "--src -" and "--out -" are sentinels for stdin/stdout rather than literal
// filenames named "-": when present, filepath.Abs and the same-path collision
// check are skipped for that side entirely, since "-" never refers to a real
// file on disk and comparing it against the other side would be meaningless.
func resolveOptions(o options) (flatten.Options, error) {
	srcIsStdin := o.src == "-"
	outIsStdout := o.out == "-" || (srcIsStdin && !o.outSet)

	var src string
	if !srcIsStdin {
		var err error
		src, err = filepath.Abs(o.src)
		if err != nil {
			return flatten.Options{}, err
		}
	}

	var out string
	if !outIsStdout {
		var err error
		out, err = filepath.Abs(o.out)
		if err != nil {
			return flatten.Options{}, err
		}
	}

	if !srcIsStdin && !outIsStdout && samePath(src, out) {
		return flatten.Options{}, &usageError{fmt.Errorf(
			"--out must not be the same file as --src (both resolve to %s); "+
				"this would overwrite the hand-authored source", filepath.Clean(src))}
	}

	root := ""
	switch {
	case o.rootSet:
		var err error
		root, err = filepath.Abs(o.root)
		if err != nil {
			return flatten.Options{}, err
		}
	case srcIsStdin:
		var err error
		root, err = os.Getwd()
		if err != nil {
			return flatten.Options{}, err
		}
	default:
		root = filepath.Dir(src)
	}

	fOpts := flatten.Options{
		SrcPath:     src,
		OutPath:     out,
		RootDir:     root,
		MaxDepth:    o.maxDepth,
		MaxDepthSet: o.maxDepthSet,
		MarkerDesc:  o.markerDesc,
		OutIsStdout: outIsStdout,
	}
	if srcIsStdin {
		fOpts.SrcPath = filepath.Join(root, "-")
		fOpts.SrcName = "-"
	} else {
		fOpts.SrcName = displayName(root, src, o.src)
	}
	if outIsStdout {
		fOpts.OutName = "-"
	} else {
		fOpts.OutName = displayName(root, out, o.out)
	}
	return fOpts, nil
}

// samePath reports whether two absolute paths name the same file on disk.
//
// The baseline comparison is on filepath.Clean'd paths, which already catches
// the common cases (identical paths, "./AGENTS.src.md" vs "AGENTS.src.md",
// redundant "." / ".." segments). We go one step further and try
// filepath.EvalSymlinks on both sides so that a symlink pointing at the other
// path is also caught (e.g. --out a-symlink-to-AGENTS.src.md).
//
// EvalSymlinks errors on a path that doesn't exist, which is expected and
// common here: --out legitimately may not exist yet (the normal "first run"
// case). So a failure to resolve either side via EvalSymlinks is treated as
// "no additional information" and we fall back to the Clean'd comparison
// instead of propagating the error — this function only needs to decide
// same-or-not, never to fail the whole resolve over a missing --out file.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	return ra == rb
}

// displayName prefers the path relative to root (forward slashes) so banners
// and status messages read the same everywhere; it falls back to whatever the
// user typed when the resolved path isn't under root at all.
//
// isParentTraversal (not a bare strings.HasPrefix(rel, "..") check) is used to
// decide "outside root": a naive prefix check would misfire on a file that is
// under root but happens to be named with a leading "..", e.g. "..foo" or
// "..bar/baz".
func displayName(root, abs, typed string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil || isParentTraversal(rel) {
		return filepath.ToSlash(typed)
	}
	return filepath.ToSlash(rel)
}

// isParentTraversal reports whether rel (as produced by filepath.Rel) climbs
// out of its base at least once, i.e. is ".." or starts with "../" (or the
// OS-specific separator equivalent). A relative path that merely starts with
// the two characters ".." — such as "..foo" — is a normal file name and does
// not count.
func isParentTraversal(rel string) bool {
	if rel == ".." {
		return true
	}
	return strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func runGenerate(fOpts flatten.Options, stdout, stderr io.Writer) int {
	content, inlined, err := flatten.Flatten(fOpts)
	if err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	var assembled string
	if fOpts.Stdin != nil {
		assembled = assembleNoBanner(content)
	} else {
		assembled = flatten.Assemble(flatten.Banner(fOpts), content)
	}

	if fOpts.OutIsStdout {
		fprint(stdout, assembled)
		return 0
	}

	// #nosec G306 -- 0o644 is the intended permission for a generated Markdown
	// doc meant to be read (and edited by hand between regenerations, before
	// the "generated" banner is noticed) like any other checked-in file.
	if err := os.WriteFile(fOpts.OutPath, []byte(assembled), 0o644); err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	fprintf(stdout, "Generated %s from %s (%d files inlined).\n",
		fOpts.OutName, fOpts.SrcName, inlined)
	return 0
}

// assembleNoBanner mirrors flatten.Assemble's trailing-newline normalization
// (exactly one trailing newline, regardless of how many the content ends
// with) but without prepending any banner text or its "\n\n" separator —
// flatten.Assemble("", content) would NOT work here, since Assemble's
// TrimRight only trims from the right, leaving stray leading blank lines
// from the empty banner + "\n\n" prefix.
func assembleNoBanner(content string) string {
	return strings.TrimRight(content, "\n") + "\n"
}

func runCheck(opts options, fOpts flatten.Options, stdout, stderr io.Writer) int {
	res, err := flatten.Check(fOpts)
	if err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	if res.UpToDate {
		fprintf(stdout, "%s is up to date.\n", fOpts.OutName)
		return 0
	}
	fprintf(stdout, "%s is out of date. Run: %s\n", fOpts.OutName, regenCommand(opts))
	if res.DiffExcerpt != "" {
		fprintf(stdout, "%s\n", res.DiffExcerpt)
	}
	return 1
}

func runListImports(fOpts flatten.Options, stdout, stderr io.Writer) int {
	var data []byte
	if fOpts.Stdin != nil {
		var err error
		data, err = io.ReadAll(fOpts.Stdin)
		if err != nil {
			fprintf(stderr, "at-include: %s\n", err)
			return 1
		}
	} else {
		// #nosec G304 -- fOpts.SrcPath is the tool's own configured source file
		// (the same path Flatten itself reads); --list-imports just needs the raw
		// text to scan for @tokens instead of the fully expanded output.
		var err error
		data, err = os.ReadFile(fOpts.SrcPath)
		if err != nil {
			fprintf(stderr, "at-include: %s\n", err)
			return 1
		}
	}
	for _, imp := range flatten.FindImports(string(data)) {
		fprintf(stdout, "%s\n", imp)
	}
	return 0
}

// fprint and fprintf write to w and deliberately discard the error. A failed
// write to the process's own stdout/stderr (e.g. a broken pipe on the
// consuming end) is not something at-include can usefully recover from or
// report — there is nowhere left to report it to — so these helpers exist
// only to give errcheck a single, explicitly-acknowledged place to point at
// instead of scattering "//nolint:errcheck" across every Fprintf call site.
func fprint(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// regenCommand renders the command that would regenerate the output file, for
// the "out of date" message.
//
// It builds the suggestion from opts — the parsed, CWD-relative command line
// the user actually typed — rather than from the resolved flatten.Options
// display names. Those display names are root-relative (for banners and
// inline markers, which must read the same regardless of the caller's CWD),
// but the user is going to paste this command into a shell sitting in their
// CWD, not in root. Building the suggestion from root-relative names is wrong
// in two ways: it prints the wrong path whenever CWD and root differ (e.g.
// `--src docs/AGENTS.src.md` sitting under a root of "." would display as the
// root-relative "AGENTS.src.md", silently collapsing to the default and
// pointing at a different file entirely), and it drops --root altogether
// (root-relative names never mention root, so a caller running with
// `--root .` would get a suggestion missing --root, which then resolves
// markers against a different root and fails --check all over again).
//
// So: echo back opts's typed values, comparing each against its default to
// decide whether to include the flag at all. One synthetic edge case is worth
// calling out: a `--marker-desc ""` that is passed explicitly is
// indistinguishable in the printed command from "not passed" UNLESS we
// consult markerDescSet, which we do (see below) — so that case is in fact
// handled correctly, unlike an earlier draft that only compared against "".
func regenCommand(opts options) string {
	parts := []string{"at-include"}
	if opts.src != defaultSrc {
		parts = append(parts, "--src", shellQuote(opts.src))
	}
	if opts.out != defaultOut {
		parts = append(parts, "--out", shellQuote(opts.out))
	}
	if opts.rootSet {
		parts = append(parts, "--root", shellQuote(opts.root))
	}
	if opts.maxDepthSet {
		parts = append(parts, "--max-depth", strconv.Itoa(opts.maxDepth))
	}
	if opts.markerDescSet {
		parts = append(parts, "--marker-desc", shellQuote(opts.markerDesc))
	}
	return strings.Join(parts, " ")
}

// shellQuote renders s as a single POSIX shell word, always via single-quote
// escaping: wrap in '...', turning each embedded single quote into the
// four-character sequence '\” (close quote, escaped literal quote, reopen
// quote). Unlike double-quoting, single-quote escaping has no live
// metacharacters inside the
// quotes at all — no $expansion, no `command` substitution, no !history
// expansion, no \escapes — so every character of s round-trips literally
// through a POSIX shell. This makes the printed "Run: ..." command not just
// display-plausible but actually safe to paste and run, which matters because
// arbitrary --marker-desc/--src/--out text (attacker- or accident-supplied)
// can contain "$", backticks, or "!" that a double-quoted rendering would
// still let the shell interpret.
//
// Values with no special characters are printed bare for readability; this is
// a cosmetic-only shortcut; POSIX single-quoting would also be correct (if
// slightly noisier) for those values.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'$`\\!*?[]{}()<>|&;~#%") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parseArgs(argv []string) (options, error) {
	o := options{src: defaultSrc, out: defaultOut}
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--check":
			o.check = true
		case "--help", "-h":
			o.help = true
		case "--version":
			o.version = true
		case "--list-imports":
			o.listImports = true
		case "--src":
			v, err := value(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.src = v
		case "--out":
			v, err := value(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.out, o.outSet = v, true
		case "--root":
			v, err := value(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.root, o.rootSet = v, true
		case "--marker-desc":
			v, err := value(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.markerDesc, o.markerDescSet = v, true
		case "--max-depth":
			var raw string
			if i+1 < len(argv) {
				raw = argv[i+1]
			}
			i++
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return o, fmt.Errorf("--max-depth requires a non-negative integer, got: %s", raw)
			}
			o.maxDepth, o.maxDepthSet = n, true
		default:
			return o, fmt.Errorf("unknown argument: %s", a)
		}
	}
	return o, nil
}

// value reads the argument following a flag, erroring if none was supplied.
func value(argv []string, i *int, flagName string) (string, error) {
	if *i+1 >= len(argv) {
		return "", fmt.Errorf("%s requires a value", flagName)
	}
	*i++
	return argv[*i], nil
}
