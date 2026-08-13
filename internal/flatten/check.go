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
// flattened source, ending in exactly one newline. When opts.Supplement is
// set, SupplementPreamble() is used in place of Banner(opts) — see
// SupplementPreamble's doc comment for why the two are not configured the
// same way.
func Generate(opts Options) (string, error) {
	content, _, err := Flatten(opts)
	if err != nil {
		return "", err
	}
	if opts.Supplement {
		return Assemble(SupplementPreamble(), content), nil
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
	// point of check.
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
// line where actual and expected disagree.
//
// A line index past the end of one side must never be treated as equal to a
// defined line on the other side. Go's zero value for a missing slice element
// would be "" (via at()/get()), which could spuriously equal a genuinely empty
// line on the other, in-range side. inBoth guards against that by tracking
// "in range" explicitly instead of relying on the zero-value fallback, so an
// out-of-range fetch on one side is never mistaken for a real match. This also
// can't produce a false "both sides missing" match while idx < n =
// max(len(a), len(e)): at least one side is always still in range in that
// window, by definition of n.
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
		// A side that is out of range never counts as equal to a defined value
		// on the other side, even when that defined value happens to be ""
		// (Go's zero value for a missing get() result). Only two in-range,
		// equal-valued sides count as "not differs".
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
// at the wrong index. Both sides being missing simultaneously never actually
// happens while idx < n = max(len(a), len(e)) — at least one side is always
// still in range in that window — so this guard never changes where the scan
// stops for a well-formed n; it exists to make that guarantee explicit rather
// than implicit in the zero-value coincidence.
func inBoth(a, e []string, i int) bool {
	return i < len(a) && i < len(e)
}
