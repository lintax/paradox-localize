package translation

import (
	"strings"
	"testing"
)

// roundTrip exercises Replace -> Restore and verifies the original text comes back unchanged.
func roundTrip(t *testing.T, input string) {
	t.Helper()
	clean, ph := ReplaceSyntaxWithPlaceholders(input)
	restored := RestorePlaceholders(clean, ph)
	if restored != input {
		t.Fatalf("round-trip mismatch:\n  input:    %q\n  clean:    %q\n  restored: %q", input, clean, restored)
	}
}

func TestReplaceSyntaxWithPlaceholders_VariableTokens(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int // expected number of placeholders
	}{
		{"single $var$", "Cost: $COST$", 1},
		{"two $vars$", "$NAME$ paid $COST$", 2},
		{"complex $var|format$", "Income: $income|0%$", 1},
		{"command bracket", "[GetModifier('boost')]", 1},
		{"icon", "@gold! per turn", 1},
		{"formatting", "#bold great choice#! indeed", 1},
		{"escaped newline", "Line1\\nLine2", 1},
		{"mixed", "Cost: $GOLD$ [GetModifier('m')] @icon!\\n#bold yes#!", 5},
		{"plain text", "Just a normal sentence.", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ph := ReplaceSyntaxWithPlaceholders(tc.input)
			if len(ph) != tc.want {
				t.Errorf("got %d placeholders, want %d (input=%q)", len(ph), tc.want, tc.input)
			}
			roundTrip(t, tc.input)
		})
	}
}

func TestReplaceSyntaxWithPlaceholders_SingleVariableLeftAlone(t *testing.T) {
	// A value that is exactly a single $...$ reference should be returned untouched
	// — translating it produces only noise.
	inputs := []string{
		"$COST$",
		"$[icon] iv2_construction$",
		"  $COST$  ", // surrounding whitespace OK
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			out, ph := ReplaceSyntaxWithPlaceholders(in)
			if out != in {
				t.Errorf("expected single-variable input to be returned unchanged, got %q", out)
			}
			if len(ph) != 0 {
				t.Errorf("expected no placeholders, got %d", len(ph))
			}
		})
	}
}

func TestReplaceSyntaxWithPlaceholders_NumbersInPlaceholdersAreSequential(t *testing.T) {
	clean, ph := ReplaceSyntaxWithPlaceholders("$A$ then $B$ then $C$")
	if len(ph) != 3 {
		t.Fatalf("expected 3 placeholders, got %d", len(ph))
	}
	for i := 0; i < 3; i++ {
		token := "{" + string(rune('0'+i)) + "}"
		if !strings.Contains(clean, token) {
			t.Errorf("clean text missing %s: %q", token, clean)
		}
	}
}

func TestIsOnlySyntax(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain text", "Hello world", false},
		{"just a variable", "$COST$", true},
		{"variable with whitespace", "  $COST$  ", true},
		{"variable name only", "iv2_construction", true},
		{"text with variable", "Cost: $COST$", false},
		{"only placeholders + punctuation", "$A$ $B$.", true},
		{"complex bracket-only", "[GetModifier('m')]", true},
		{"escaped newline only", "\\n\\n", true},
		{"text with newlines", "Hello\\nworld", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, ph := ReplaceSyntaxWithPlaceholders(tc.input)
			got := IsOnlySyntax(clean, ph)
			if got != tc.want {
				t.Errorf("IsOnlySyntax(%q) = %v, want %v (clean=%q, placeholders=%v)",
					tc.input, got, tc.want, clean, ph)
			}
		})
	}
}

func TestRestorePlaceholders_DoesNotMatchPartialTokens(t *testing.T) {
	// {0}, {1}, ... {10} — make sure {1} doesn't accidentally match {10}.
	input := "$A$ $B$ $C$ $D$ $E$ $F$ $G$ $H$ $I$ $J$ $K$"
	clean, ph := ReplaceSyntaxWithPlaceholders(input)
	if len(ph) != 11 {
		t.Fatalf("expected 11 placeholders, got %d", len(ph))
	}
	restored := RestorePlaceholders(clean, ph)
	if restored != input {
		t.Errorf("round-trip with 10+ placeholders failed:\n  in:  %q\n  out: %q", input, restored)
	}
}

func TestCleanLLMResponse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello world", "hello world"},
		{"trim spaces", "   hi   ", "hi"},
		{"strip double quotes", `"hello"`, "hello"},
		{"strip single quotes", "'hello'", "hello"},
		{"strip code fence", "```\nhello\n```", "hello"},
		{"strip think block", "<think>reasoning</think>привет", "привет"},
		{"french guillemets", "«hi»", "hi"},
		{"keep inner quotes", `say "hello" loudly`, `say "hello" loudly`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanLLMResponse(tc.input); got != tc.want {
				t.Errorf("cleanLLMResponse(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
