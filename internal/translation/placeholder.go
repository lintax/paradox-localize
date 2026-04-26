package translation

import (
	"fmt"
	"regexp"
	"strings"
)

var paradoxSyntaxPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\$[^\$\s]+\$`),       // $VARIABLE$ or $NUM|V%$ - anything between $ symbols without spaces
	regexp.MustCompile(`\[[^\]]+\]`),         // [GetModifier(...)]
	regexp.MustCompile(`@[a-z_][a-z0-9_]*!`), // @icon!
	regexp.MustCompile(`#[a-z_]+\s+.*?#!`),   // #bold text#!
	regexp.MustCompile(`\\n`),                // \n
}

// ReplaceSyntaxWithPlaceholders replaces Paradox syntax with XML-style placeholders
// Returns the modified text and a map of placeholders to original syntax
func ReplaceSyntaxWithPlaceholders(text string) (string, map[string]string) {
	placeholders := make(map[string]string)
	result := text
	counter := 0

	// IMPORTANT: Check if entire text is a SINGLE $...$ variable before processing
	// Pattern: $[anything without another $]$
	// Can contain spaces and syntax like: "$[icon] iv2_construction$"
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 2 && strings.HasPrefix(trimmed, "$") && strings.HasSuffix(trimmed, "$") {
		// Check if there's no other $ symbol in the middle
		inner := trimmed[1 : len(trimmed)-1]
		if !strings.Contains(inner, "$") {
			// This is a single Paradox variable like "$something$" or "$[icon] var$" - don't process it
			return text, placeholders
		}
	}

	for _, pattern := range paradoxSyntaxPatterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			placeholder := fmt.Sprintf("{%d}", counter)
			placeholders[placeholder] = match
			counter++
			return placeholder
		})
	}

	return result, placeholders
}

// RestorePlaceholders replaces placeholders back with original Paradox syntax
func RestorePlaceholders(text string, placeholders map[string]string) string {
	if len(placeholders) == 0 {
		return text
	}
	// Use strings.Replacer for single-pass replacement
	pairs := make([]string, 0, len(placeholders)*2)
	for placeholder, original := range placeholders {
		pairs = append(pairs, placeholder, original)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// IsOnlySyntax checks if a string contains ONLY Paradox syntax (no translatable text)
func IsOnlySyntax(text string, placeholders map[string]string) bool {
	trimmed := strings.TrimSpace(text)

	// Check if entire text is a SINGLE $...$ variable (Paradox variable reference)
	// Can contain spaces and syntax like: "$[icon] iv2_construction$"
	if len(trimmed) > 2 && strings.HasPrefix(trimmed, "$") && strings.HasSuffix(trimmed, "$") {
		// Check if there's no other $ symbol in the middle
		inner := trimmed[1 : len(trimmed)-1]
		if !strings.Contains(inner, "$") {
			return true
		}
	}

	textWithoutSyntax := text
	for placeholder := range placeholders {
		textWithoutSyntax = strings.ReplaceAll(textWithoutSyntax, placeholder, "")
	}
	// \n is already captured by paradoxSyntaxPatterns and replaced above.
	// \r and \t are not, so strip them here.
	textWithoutSyntax = strings.ReplaceAll(textWithoutSyntax, "\\r", "")
	textWithoutSyntax = strings.ReplaceAll(textWithoutSyntax, "\\t", "")

	// Trim whitespace and check what remains
	remaining := strings.TrimSpace(textWithoutSyntax)

	// If nothing left, or only punctuation, it's syntax-only
	if remaining == "" || len(strings.Trim(remaining, ".,!?;:- ")) == 0 {
		return true
	}

	// If remaining text contains $ symbols, it's likely incomplete syntax (e.g., "$var_name$" with spaces broken up)
	if strings.Contains(remaining, "$") {
		return true
	}

	// If remaining text is all lowercase with underscores/numbers (like "iv2_construction"), it's likely a variable name
	// Variable names in code: snake_case, no spaces, no uppercase at word boundaries
	if isVariableName(remaining) {
		return true
	}

	return false
}

// isVariableName reports whether s looks like an internal Paradox identifier
// rather than translatable text. A real variable in Paradox files is snake_case
// (e.g. "iv2_construction") or SCREAMING_SNAKE_CASE — both contain at least one
// underscore. Single English words like "Helloworld" must NOT match here, or the
// LLM will skip them as syntax.
func isVariableName(s string) bool {
	if s == "" || !strings.ContainsRune(s, '_') {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}
