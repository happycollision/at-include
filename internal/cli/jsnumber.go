package cli

import (
	"math"
	"strconv"
	"strings"
)

// This file is a self-contained port of the JS CLI's --max-depth validation
// semantics — how build-agents.mjs's `Number(v)` parses a raw flag value. It
// is split out from cli.go (rather than living inline with parseArgs) because
// it already has its own test file, maxdepth_test.go; keeping the file
// boundary aligned with the test boundary makes it easy to see the whole
// ported semantics, and its tests, together.
//
// Deviations from real JS Number() semantics that this port does NOT
// reproduce (both are also documented on parseNonNegativeIntJS below, next
// to the divergences it DOES call out):
//   - Values >= 2^31 are rejected here where JS's Number()/--max-depth check
//     would accept them (JS has no integer-width limit; this code caps at
//     math.MaxInt32 because the rest of the Go program stores MaxDepth as an
//     int and a --max-depth this large is not meaningful in practice).
//   - A value with a leading U+FEFF (BOM) codepoint before the digits is
//     rejected here. JS's String.prototype.trim() strips U+FEFF as part of its \s-plus-line-
//     terminator whitespace set (see scan.go's jsIsSpace), so JS would accept
//     it as "5"; this port's trimming is strings.TrimSpace, which is
//     ASCII-only and leaves the BOM in place, so parseJSNumber then rejects
//     the resulting string as non-numeric.
//
// Both are accepted, documented divergences: a --max-depth flag this
// pathological is not a realistic CLI input, and fixing either would add
// meaningfully more code for no practical benefit.

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
//   - Values >= 2^31: see the file-level doc comment above.
//   - A leading U+FEFF (BOM): see the file-level doc comment above.
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
