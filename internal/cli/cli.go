// Package cli implements the at-include command line: argument parsing and
// the generate/--check/--list-imports/--version/--help behaviors described in
// the design spec. It is a thin wrapper over internal/flatten — all of the
// actual import-flattening logic lives there.
package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/happycollision/at-include/internal/flatten"
)

// Version is overridden at build time with -ldflags "-X ...cli.Version=v1.2.3".
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
	check       bool
	help        bool
	version     bool
	listImports bool
	src         string
	out         string
	root        string
	rootSet     bool
	markerDesc  string
	maxDepth    int
	maxDepthSet bool
}

// Run parses argv (excluding the program name), performs the requested
// action, and returns the process exit code. Relative --src/--out/--root
// values resolve against the process's current working directory, mirroring
// the JS's process.cwd()-relative resolution.
func Run(argv []string, stdout, stderr io.Writer) int {
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
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}

	switch {
	case opts.listImports:
		return runListImports(fOpts, stdout, stderr)
	case opts.check:
		return runCheck(fOpts, stdout, stderr)
	default:
		return runGenerate(fOpts, stdout, stderr)
	}
}

// resolveOptions turns CLI strings into absolute paths and display names.
func resolveOptions(o options) (flatten.Options, error) {
	src, err := filepath.Abs(o.src)
	if err != nil {
		return flatten.Options{}, err
	}
	out, err := filepath.Abs(o.out)
	if err != nil {
		return flatten.Options{}, err
	}
	root := filepath.Dir(src)
	if o.rootSet {
		if root, err = filepath.Abs(o.root); err != nil {
			return flatten.Options{}, err
		}
	}
	return flatten.Options{
		SrcPath:     src,
		OutPath:     out,
		RootDir:     root,
		MaxDepth:    o.maxDepth,
		MaxDepthSet: o.maxDepthSet,
		MarkerDesc:  o.markerDesc,
		SrcName:     displayName(root, src, o.src),
		OutName:     displayName(root, out, o.out),
	}, nil
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
	assembled := flatten.Assemble(flatten.Banner(fOpts), content)
	// #nosec G306 -- 0o644 is the intended permission for a generated Markdown
	// doc meant to be read (and edited by hand between regenerations, before
	// the "generated" banner is noticed) like any other checked-in file; it
	// mirrors the JS's writeFileSync(outPath, ...) with Node's default mode.
	if err := os.WriteFile(fOpts.OutPath, []byte(assembled), 0o644); err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	fprintf(stdout, "Generated %s from %s (%d files inlined).\n",
		fOpts.OutName, fOpts.SrcName, inlined)
	return 0
}

func runCheck(fOpts flatten.Options, stdout, stderr io.Writer) int {
	res, err := flatten.Check(fOpts)
	if err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
	}
	if res.UpToDate {
		fprintf(stdout, "%s is up to date.\n", fOpts.OutName)
		return 0
	}
	fprintf(stdout, "%s is out of date. Run: %s\n", fOpts.OutName, regenCommand(fOpts))
	if res.DiffExcerpt != "" {
		fprintf(stdout, "%s\n", res.DiffExcerpt)
	}
	return 1
}

func runListImports(fOpts flatten.Options, stdout, stderr io.Writer) int {
	// #nosec G304 -- fOpts.SrcPath is the tool's own configured source file
	// (the same path Flatten itself reads); --list-imports just needs the raw
	// text to scan for @tokens instead of the fully expanded output.
	data, err := os.ReadFile(fOpts.SrcPath)
	if err != nil {
		fprintf(stderr, "at-include: %s\n", err)
		return 1
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

// regenCommand renders the command that would regenerate fOpts.OutName from
// fOpts.SrcName, for the "out of date" message.
//
// It always names the *resolved* --src/--out/--root/--marker-desc/--max-depth
// this run actually used (via fOpts, which already reflects fOpts.SrcName's
// display form and so on) rather than echoing back the caller's raw argv.
// Echoing raw argv back (as an earlier draft did) has two problems: (1) it
// under-reports whenever a flag's value happens to equal that flag's default
// — e.g. `--marker-desc ""` is indistinguishable from "not passed" if the
// check is `markerDesc != ""` — and (2) whatever we print has to be
// shell-quoted correctly for values containing spaces or quotes, which is one
// more way to print a subtly wrong command. Since this function only runs
// after resolveOptions has already computed fOpts, it always reflects the
// actual configuration of this run, and only needs to quote values it prints.
func regenCommand(fOpts flatten.Options) string {
	parts := []string{"at-include"}
	if fOpts.SrcName != defaultSrc {
		parts = append(parts, "--src", quoteArg(fOpts.SrcName))
	}
	if fOpts.OutName != defaultOut {
		parts = append(parts, "--out", quoteArg(fOpts.OutName))
	}
	if fOpts.MaxDepthSet {
		parts = append(parts, "--max-depth", strconv.Itoa(fOpts.MaxDepth))
	}
	if fOpts.MarkerDesc != "" {
		parts = append(parts, "--marker-desc", quoteArg(fOpts.MarkerDesc))
	}
	return strings.Join(parts, " ")
}

// quoteArg wraps a value in double quotes whenever it contains characters
// that would otherwise split it into multiple shell words. This is a display
// aid for the "Run: ..." message, not a real shell-escaping routine — it does
// not need to handle embedded double quotes specially, because it exists only
// to make the common case (a path with spaces) copy-pasteable, not to
// guarantee round-tripping of arbitrary strings.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\") {
		return s
	}
	return strconv.Quote(s)
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
			o.out = v
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
			o.markerDesc = v
		case "--max-depth":
			var raw string
			if i+1 < len(argv) {
				raw = argv[i+1]
			}
			i++
			n, ok := parseNonNegativeIntJS(raw)
			if !ok {
				return o, fmt.Errorf("--max-depth requires a non-negative integer, got: %s", raw)
			}
			o.maxDepth, o.maxDepthSet = n, true
		default:
			return o, fmt.Errorf("Unknown argument: %s", a) //nolint:staticcheck // ST1005: matches the JS spec's exact "Unknown argument: %s" message verbatim (see the precedent on expand.go's depth-exceeded error).
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

// parseNonNegativeIntJS parses raw the way the JS CLI's --max-depth validation
// does:
//
//	const n = v === undefined || v.trim() === "" ? NaN : Number(v);
//	if (!Number.isInteger(n) || n < 0) { ... error ... }
//
// JS Number() accepts a much wider grammar than strconv.Atoi: surrounding
// whitespace, a leading "+", decimal points that round-trip to an integer
// ("3.0", "3."), scientific notation ("1e2"), and unsigned hex/octal/binary
// prefixes ("0x10", "0o17", "0b101"). This function reproduces all of that
// (verified against a Node repl for every case in the table in cli_test.go's
// TestParseNonNegativeIntJS, plus the doc comment's own worked examples), with
// one deliberate, narrow divergence documented below.
//
// Deliberate divergences from real Number() semantics:
//   - "-0x1", "+0b1": JS Number() rejects a sign in front of a 0x/0o/0b
//     prefix (Number("-0x1") is NaN — the sign-then-radix-prefix combination
//     isn't part of the StringNumericLiteral grammar at all, only inside
//     actual JS source as `-` applied to a numeric literal). This function
//     also rejects a signed prefixed literal, so behavior matches; this isn't
//     actually a divergence, but is called out because it was tempting to
//     "fix" by allowing the sign, which would NOT match the JS.
//   - "1_000": JS Number() rejects numeric separators outright (they're only
//     legal in source-code numeric literals, not in Number()'s string
//     grammar). Go's strconv.ParseFloat/ParseInt, by contrast, both accept
//     underscore digit separators (matching Go's own numeric-literal syntax),
//     so parseJSNumber explicitly rejects any '_' before delegating to them —
//     without that guard this function would wrongly accept "1_000".
//   - Whitespace trimmed here is ASCII-only (strings.TrimSpace's Unicode set,
//     specifically); JS's String.prototype.trim() strips the same broader
//     \s-plus-line-terminator set as scan.go's jsIsSpace. A --max-depth value
//     padded with, say, a non-breaking space is vanishingly unlikely in
//     practice for a CLI integer flag, so this narrow gap is accepted rather
//     than pulling jsIsSpace's trimming into this unrelated parser.
func parseNonNegativeIntJS(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}

	f, ok := parseJSNumber(trimmed)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f != math.Trunc(f) || f < 0 {
		return 0, false
	}
	// f is a non-negative integer value; guard against magnitudes that would
	// overflow int (the JS side has no such limit, but a --max-depth this
	// large is not meaningful and int is what the rest of the Go code uses).
	if f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

// parseJSNumber parses a (pre-trimmed, non-empty) numeric string the way JS's
// Number(string) does for the forms --max-depth actually needs: signed
// decimal integers, decimals with a trailing/leading '.', scientific
// notation, and unsigned 0x/0o/0b-prefixed integers.
func parseJSNumber(s string) (float64, bool) {
	// Go's strconv.ParseFloat/ParseInt both accept '_' digit separators
	// (Go's own numeric-literal syntax); JS Number() does not accept them
	// under any circumstance. Reject up front so "1_000" doesn't silently
	// parse as 1000.
	if strings.ContainsRune(s, '_') {
		return 0, false
	}
	if hasUnsignedRadixPrefix(s) {
		n, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, false
		}
		return float64(n), true
	}
	// strconv.ParseFloat already matches Number()'s decimal/scientific/
	// Infinity/NaN grammar closely enough for this CLI's purposes: both
	// accept an optional leading sign, digits, a decimal point in any
	// position ("3.", ".5"), and an exponent; both parse "Infinity"/"NaN".
	// It also accepts a Go-only hex-float form ("0x1p10") that JS would
	// reject, but that isn't reachable here: hasUnsignedRadixPrefix already
	// routed any "0x..."/"0X..." input through ParseInt above, so a hex-float
	// literal reaches ParseFloat only if it lacks the "0x" prefix, which is
	// impossible for that syntax. So no separate guard is needed for it.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// hasUnsignedRadixPrefix reports whether s looks like an (unsigned) 0x/0o/0b
// integer literal. JS Number() only recognizes these prefixes without a sign;
// "-0x10" and "+0b1" are NaN in JS, so a leading sign here is deliberately
// excluded rather than handled.
func hasUnsignedRadixPrefix(s string) bool {
	if len(s) < 3 || s[0] != '0' {
		return false
	}
	switch s[1] {
	case 'x', 'X', 'o', 'O', 'b', 'B':
		return true
	default:
		return false
	}
}
