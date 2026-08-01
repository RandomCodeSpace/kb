package board

import "testing"

func TestJavaScriptWhitespaceHelpers(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		containsSpace bool
		blank         bool
		singleEmoji   bool
	}{
		{name: "empty is blank", value: "", blank: true},
		{name: "javascript spaces are blank", value: "\u00a0\u2003\ufeff", containsSpace: true, blank: true},
		{name: "word with space", value: "hello world", containsSpace: true},
		{name: "plain word", value: "hello"},
		{name: "one emoji", value: "🚀", singleEmoji: true},
		{name: "emoji with variation selector", value: "❤️", singleEmoji: true},
		{name: "two emoji", value: "🚀🚀"},
		{name: "emoji plus text", value: "🚀 ship", containsSpace: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsSpace(tt.value); got != tt.containsSpace {
				t.Errorf("ContainsSpace(%q) = %v, want %v", tt.value, got, tt.containsSpace)
			}
			if got := IsBlank(tt.value); got != tt.blank {
				t.Errorf("IsBlank(%q) = %v, want %v", tt.value, got, tt.blank)
			}
			if got := IsSingleEmoji(tt.value); got != tt.singleEmoji {
				t.Errorf("IsSingleEmoji(%q) = %v, want %v", tt.value, got, tt.singleEmoji)
			}
		})
	}
}
