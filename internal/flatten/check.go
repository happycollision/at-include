package flatten

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// CheckResult reports whether the output file on disk matches what a fresh run
// would produce.
type CheckResult struct {
	UpToDate    bool
	Missing     bool   // the output file does not exist
	DiffExcerpt string // a short excerpt around the first difference
}

// Generate returns the full output text: banner, a blank line, then the
// flattened source, ending in exactly one newline.
func Generate(opts Options) (string, error) {
	content, _, err := Flatten(opts)
	if err != nil {
		return "", err
	}
	return Assemble(Banner(opts), content), nil
}

// Check compares freshly generated output against opts.OutPath. A missing
// output file is reported as stale, not as an error.
func Check(opts Options) (CheckResult, error) {
	expected, err := Generate(opts)
	if err != nil {
		return CheckResult{}, err
	}
	// #nosec G304 G703 -- opts.OutPath is the tool's own configured output
	// path; reading it back to compare against a fresh render is the entire
	// point of --check.
	actual, err := os.ReadFile(opts.OutPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CheckResult{UpToDate: false, Missing: true}, nil
		}
		return CheckResult{}, err
	}
	if string(actual) == expected {
		return CheckResult{UpToDate: true}, nil
	}
	return CheckResult{UpToDate: false, DiffExcerpt: firstDiffExcerpt(string(actual), expected)}, nil
}

// firstDiffExcerpt renders a small unified-diff-style excerpt around the first
// line where actual and expected disagree. Ports the JS firstDiffExcerpt
// exactly, including its undefined-vs-defined comparison semantics: a line
// index past the end of one side is never treated as equal to a defined line
// on the other side, and (since the scan never proceeds past n =
// max(len(a), len(e))) is never treated as equal to a matching past-the-end
// index on the other side either, because both sides cannot be
// simultaneously past their own length while an index is still < n. inBoth
// makes that guarantee explicit instead of relying on a Go zero-value
// (empty-string) stand-in for JS's `undefined`, which would otherwise let two
// out-of-range fetches compare spuriously equal.
func firstDiffExcerpt(actual, expected string) string {
	a := strings.Split(actual, "\n")
	e := strings.Split(expected, "\n")
	n := max(len(a), len(e))

	idx := 0
	for idx < n && inBoth(a, e, idx) && at(a, idx) == at(e, idx) {
		idx++
	}

	const ctx = 2
	var lines []string
	for k := max(0, idx-ctx); k <= idx+ctx && k < n; k++ {
		av, aOK := get(a, k)
		ev, eOK := get(e, k)
		// differs mirrors JS's `a[k] !== e[k]`: a side that is out of range
		// (JS undefined) never equals a defined value on the other side, even
		// when that defined value happens to be "" (Go's zero value for a
		// missing get() result). Only two in-range, equal-valued sides count
		// as "not differs".
		differs := !aOK || !eOK || av != ev
		switch {
		case !differs:
			lines = append(lines, "  "+av)
		default:
			if aOK && differs {
				lines = append(lines, "- "+av)
			}
			if eOK && differs {
				lines = append(lines, "+ "+ev)
			}
		}
	}
	return fmt.Sprintf("First difference around line %d:\n%s", idx+1, strings.Join(lines, "\n"))
}

func get(s []string, i int) (string, bool) {
	if i < len(s) {
		return s[i], true
	}
	return "", false
}

func at(s []string, i int) string {
	v, _ := get(s, i)
	return v
}

// inBoth reports whether index i is within range for both slices. Guards the
// while-loop scan against the case where a[i] and e[i] are both "missing" (one
// or both slices too short): without this guard, Go's zero-value fallback for
// a missing index (empty string, from at()) could compare equal to a
// genuinely empty line on the other, in-range side, which would stop the scan
// at the wrong index. JS's `a[idx] === e[idx]` never has this problem because
// a missing index is `undefined`, which strictly equals only another
// `undefined` (i.e. only when BOTH sides are missing) — and both sides being
// missing simultaneously never happens while idx < n = max(len(a), len(e)),
// so inBoth reproduces the JS stopping point exactly for every input.
func inBoth(a, e []string, i int) bool {
	return i < len(a) && i < len(e)
}
