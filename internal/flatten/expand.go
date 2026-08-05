package flatten

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// DefaultMarkerDesc is the parenthetical Claude Code uses when it inlines a
// project instruction file.
const DefaultMarkerDesc = "project instructions, checked into the codebase"

// Options configures a flattening run. Paths must be absolute; RootDir decides
// how paths are rendered in the inline markers.
//
// SrcName, OutName, and OutPath are consumed by the banner/check/CLI layers;
// Flatten itself uses only SrcPath, RootDir, MaxDepth/MaxDepthSet, and
// MarkerDesc.
type Options struct {
	SrcPath     string
	OutPath     string
	RootDir     string
	MaxDepth    int
	MaxDepthSet bool
	MarkerDesc  string
	SrcName     string
	OutName     string
}

func (o Options) markerDesc() string {
	if o.MarkerDesc == "" {
		return DefaultMarkerDesc
	}
	return o.MarkerDesc
}

// defaultSrcName and defaultOutName are the same fallback display names
// cli.go's defaultSrc/defaultOut constants use. They are duplicated here
// (rather than imported, which would invert the cli->flatten dependency)
// because Options is a flatten type and must be able to default its own
// display names without depending on the cli package.
const (
	defaultSrcName = "AGENTS.src.md"
	defaultOutName = "AGENTS.md"
)

// srcName returns o.SrcName, defaulting to "AGENTS.src.md" when unset, so
// callers that render Options for display (Banner) never need to duplicate
// that default themselves.
func (o Options) srcName() string {
	if o.SrcName == "" {
		return defaultSrcName
	}
	return o.SrcName
}

// outName is srcName's counterpart for OutName.
func (o Options) outName() string {
	if o.OutName == "" {
		return defaultOutName
	}
	return o.OutName
}

// expander carries the state that must be shared across the whole recursive
// walk: which files have already been inlined in full, and how many were.
//
// Not safe for concurrent use: Flatten creates a fresh expander per call, and
// its inlined/count state is mutated in place during the recursive walk.
type expander struct {
	opts    Options
	inlined map[string]bool
	count   int
}

// Flatten reads opts.SrcPath and returns its text with every resolvable @path
// import replaced by the imported file's (recursively flattened) contents.
//
// Each file's content is inlined at most once across the whole output; later
// references — including cycle back-edges — emit an "already inlined above"
// marker instead, which is what makes cyclic imports terminate.
func Flatten(opts Options) (string, int, error) {
	e := &expander{opts: opts, inlined: map[string]bool{}}
	content, err := e.expandFile(opts.SrcPath, nil)
	if err != nil {
		return "", 0, err
	}
	return content, e.count, nil
}

// expandFile reads absPath and returns its transformed text. stack holds the
// ancestor paths on the current import chain; its length is the hop count.
// The contents of stack are currently unused beyond their length — they are
// kept for JS parity (JS builds the same [...stack, importerAbsPath] chain)
// and to enable a future import-chain trace in error messages, so don't
// "clean up" stack into a bare counter.
func (e *expander) expandFile(absPath string, stack []string) (string, error) {
	if e.opts.MaxDepthSet && len(stack) > e.opts.MaxDepth {
		// Capital "Import" is intentional: it must match the JS spec's error
		// text (build-agents.mjs's `Import depth ${...} exceeds --max-depth
		// ${...} at ${...}`) verbatim, since the CLI prints it through
		// unchanged. This violates the Go convention that error strings
		// shouldn't be capitalized (ST1005), but fidelity to the spec wins.
		//nolint:staticcheck // ST1005: capitalization is mandated by the JS spec's exact error text.
		return "", fmt.Errorf("Import depth %d exceeds --max-depth %d at %s",
			len(stack), e.opts.MaxDepth, e.toRootRel(absPath))
	}
	// #nosec G304 G703 -- absPath is a user-specified @import target; reading
	// arbitrary caller-chosen files is this tool's entire purpose (mirrors
	// JS readFileSync(absPath, "utf8")).
	//
	// Node's readFileSync(path, "utf8") decodes file contents as UTF-8 with
	// U+FFFD replacement for invalid byte sequences; this port passes the raw
	// bytes through unchanged (see the code-span, token, and default-rune
	// paths in transformLine). For valid UTF-8 input — the realistic case for
	// instruction files — the two are identical. Invalid UTF-8 is an accepted,
	// documented deviation: we intentionally do not add a replacement-decoding
	// pass to the hot path for a case that doesn't occur in practice.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return e.transform(string(data), absPath, stack)
}

// transform rewrites a file's text line by line, copying fenced code blocks
// through verbatim.
func (e *expander) transform(text, importerAbsPath string, stack []string) (string, error) {
	importerDir := filepath.Dir(importerAbsPath)
	var fence fenceState
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		isDelim, inFence := fence.step(line)
		if isDelim || inFence {
			out = append(out, line)
			continue
		}
		expanded, err := e.transformLine(line, importerDir, stack, importerAbsPath)
		if err != nil {
			return "", err
		}
		out = append(out, expanded)
	}
	return strings.Join(out, "\n"), nil
}

// transformLine rebuilds one line: inline code spans are kept verbatim
// (backticks included) and resolvable @tokens are replaced.
//
// Token scanning is rune-aware and uses jsIsSpace, matching JS
// transformLine's `/\s/.test(line[j])` under Unicode semantics — the same
// whitespace definition the scanner (scan.go) uses. Note the deliberate
// asymmetry with FindImports documented on that function: this scan does NOT
// strip inline code spans first, so a token can run through backticks (see
// the case '`' handling below only splits spans that start at the top level;
// once inside an '@' token, backticks are just non-whitespace characters).
func (e *expander) transformLine(line, importerDir string, stack []string, importerAbsPath string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(line); {
		switch line[i] {
		case '`':
			ticks := 0
			for i < len(line) && line[i] == '`' {
				ticks++
				i++
			}
			closer := strings.Repeat("`", ticks)
			closeIdx := strings.Index(line[i:], closer)
			if closeIdx == -1 {
				b.WriteString(closer) // unterminated run: literal backticks
				continue
			}
			b.WriteString(closer + line[i:i+closeIdx] + closer)
			i += closeIdx + ticks
		case '@':
			j := i + 1
			for j < len(line) {
				r, size := utf8.DecodeRuneInString(line[j:])
				if jsIsSpace(r) {
					break
				}
				j += size
			}
			token := line[i+1 : j]
			expansion, ok, err := e.tryExpandToken(token, importerDir, stack, importerAbsPath)
			if err != nil {
				return "", err
			}
			if ok {
				b.WriteString(expansion)
			} else {
				b.WriteString("@" + token)
			}
			i = j
		default:
			r, size := utf8.DecodeRuneInString(line[i:])
			b.WriteRune(r)
			i += size
		}
	}
	return b.String(), nil
}

// tryExpandToken resolves a single @token. ok is false when the token should be
// left in the output as literal text, which is the case for anything that is not
// an existing regular file.
func (e *expander) tryExpandToken(token, importerDir string, stack []string, importerAbsPath string) (expansion string, ok bool, err error) {
	if token == "" {
		return "", false, nil
	}
	abs := token
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(importerDir, filepath.FromSlash(token))
	}
	// #nosec G304 G703 -- abs is derived from a user-specified @import token;
	// statting arbitrary caller-chosen paths to check file existence/type is
	// this tool's entire purpose (mirrors JS existsSync/statSync on abs).
	info, statErr := os.Stat(abs)
	if statErr != nil || !info.Mode().IsRegular() {
		// Deliberately swallowing statErr here (rather than returning it) is JS
		// semantics, not an oversight: the JS spec treats "the token doesn't
		// resolve to a file" (whether because it doesn't exist, a parent
		// directory in the path doesn't exist, or a permission error prevents
		// stat'ing it) as "leave the @token as literal text", never as a hard
		// failure of the whole run. So every os.Stat error here — not just
		// os.ErrNotExist — is folded into the same "unresolvable" outcome.
		//nolint:nilerr // statErr is intentionally discarded: unresolvable token -> literal text, per JS spec.
		return "", false, nil
	}

	rootRel := e.toRootRel(abs)
	if e.inlined[abs] {
		return fmt.Sprintf("Contents of %s (already inlined above):", rootRel), true, nil
	}
	e.inlined[abs] = true
	e.count++

	inner, err := e.expandFile(abs, append(append([]string{}, stack...), importerAbsPath))
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("Contents of %s (%s):\n\n%s",
		rootRel, e.opts.markerDesc(), ensureTrailingNewline(inner)), true, nil
}

// toRootRel renders abs relative to RootDir with forward slashes, so generated
// markers are identical on every platform. Matches JS
// `relative(rootDir, abs).split("\\").join("/")`, including its behavior for
// paths outside rootDir (a leading "../" segment) — filepath.Rel handles that
// the same way Node's path.relative does, so no special-casing is needed.
func (e *expander) toRootRel(abs string) string {
	rel, err := filepath.Rel(e.opts.RootDir, abs)
	if err != nil {
		// filepath.Rel only errs when the two paths can't be made relative
		// (e.g. different volumes on Windows); Node's path.relative has no
		// such failure mode. Fall back to the absolute path, slash-normalized,
		// which is the closest faithful behavior available.
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
