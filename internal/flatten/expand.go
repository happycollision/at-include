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

// expander carries the state that must be shared across the whole recursive
// walk: which files have already been inlined in full, and how many were.
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
func (e *expander) expandFile(absPath string, stack []string) (string, error) {
	if e.opts.MaxDepthSet && len(stack) > e.opts.MaxDepth {
		return "", fmt.Errorf("import depth %d exceeds --max-depth %d at %s",
			len(stack), e.opts.MaxDepth, e.toRootRel(absPath))
	}
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
func (e *expander) tryExpandToken(token, importerDir string, stack []string, importerAbsPath string) (string, bool, error) {
	if token == "" {
		return "", false, nil
	}
	abs := token
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(importerDir, filepath.FromSlash(token))
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return "", false, nil // unresolvable or not a file: literal text, never an error
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
