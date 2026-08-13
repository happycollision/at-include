package flatten

import (
	"strings"
	"testing"
)

func TestSupplementPreambleMentionsImportSyntaxAndUnresolvedPointer(t *testing.T) {
	t.Parallel()
	p := SupplementPreamble()
	for _, want := range []string{
		"@path",
		"AGENTS.md",
		"pre-expanded",
		"@import",
		"go read that file yourself",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("preamble missing %q\ngot:\n%s", want, p)
		}
	}
	if strings.HasSuffix(p, "\n") {
		t.Error("preamble should not end with a newline; Assemble adds the separator")
	}
}

func TestSupplementPreambleIsFixedRegardlessOfOptions(t *testing.T) {
	t.Parallel()
	first := SupplementPreamble()
	second := SupplementPreamble()
	if first != second {
		t.Error("SupplementPreamble should return identical text on every call")
	}
}
