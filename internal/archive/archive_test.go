package archive

import "testing"

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Reduce p95 latency!":    "reduce-p95-latency",
		"  API cost / retries  ": "api-cost-retries",
		"":                       "optimization",
		"Already-clean-slug":     "already-clean-slug",
		"symbols *** everywhere": "symbols-everywhere",
	}

	for input, want := range tests {
		if got := Slug(input, 64); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSlugMaxLength(t *testing.T) {
	got := Slug("make the checkout endpoint significantly faster", 12)
	if got != "make-the" {
		t.Fatalf("Slug max length = %q, want %q", got, "make-the")
	}
}
