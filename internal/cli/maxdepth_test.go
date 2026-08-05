package cli

import "testing"

// TestParseNonNegativeIntJS pins parseNonNegativeIntJS against the actual
// output of the JS validation expression it ports:
//
//	const n = v === undefined || v.trim() === "" ? NaN : Number(v);
//	const valid = Number.isInteger(n) && n >= 0;
//
// Every row below was captured by running that exact expression in Node
// (not transcribed by hand), via:
//
//	node -e '
//	function test(v) {
//	  const n = v === undefined || v.trim() === "" ? NaN : Number(v);
//	  const valid = Number.isInteger(n) && n >= 0;
//	  return {v, valid, n};
//	}
//	[...].forEach(c => console.log(test(c)));
//	'
//
// This test does not call t.Parallel(): it is pure and side-effect free, but
// is kept consistent with the rest of this file's no-parallel convention
// (see the package doc comment) since nothing here needs to run concurrently.
func TestParseNonNegativeIntJS(t *testing.T) {
	cases := []struct {
		raw   string
		valid bool
		want  int // meaningful only when valid is true
	}{
		{"0", true, 0},
		{"1", true, 1},
		{"007", true, 7},
		{"  3  ", true, 3},
		{"1e2", true, 100},
		{"1E2", true, 100},
		{"0x10", true, 16},
		{"0X10", true, 16},
		{"0b101", true, 5},
		{"0B101", true, 5},
		{"0o17", true, 15},
		{"0O17", true, 15},
		{"+3", true, 3},
		{"-3", false, 0},
		{"3.0", true, 3},
		{"3.", true, 3},
		{".5", false, 0},
		{"1.5", false, 0},
		{"Infinity", false, 0},
		{"-Infinity", false, 0},
		{"NaN", false, 0},
		{"", false, 0},
		{"  ", false, 0},
		{"1_000", false, 0}, // divergence note: Go's own float/int parsers would
		// accept "1_000" (digit-separator syntax); parseJSNumber avoids ParseInt's
		// separator support for exactly this reason. See parseJSNumber's doc.
		{"-0x1", false, 0}, // JS Number() rejects a sign before a radix prefix.
		{"+0b1", false, 0}, // same.
		{"2", true, 2},
		{"-1", false, 0},
		{"notanumber", false, 0},
	}
	for _, tc := range cases {
		gotN, gotOK := parseNonNegativeIntJS(tc.raw)
		if gotOK != tc.valid {
			t.Errorf("parseNonNegativeIntJS(%q) ok = %v, want %v", tc.raw, gotOK, tc.valid)
			continue
		}
		if tc.valid && gotN != tc.want {
			t.Errorf("parseNonNegativeIntJS(%q) = %d, want %d", tc.raw, gotN, tc.want)
		}
	}
}
