package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoFunctionSignaturesIgnoreParameterNames(t *testing.T) {
	dir := t.TempDir()
	source := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart) (total Money, err error) { return 0, nil }
`
	if err := os.WriteFile(filepath.Join(dir, "pricing.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	signatures, err := goFunctionSignatures(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "func(Cart) (Money, error)"
	if signatures["PriceCheckout"] != want {
		t.Fatalf("PriceCheckout signature = %q, want %q", signatures["PriceCheckout"], want)
	}
}

func TestDropInFunctionName(t *testing.T) {
	for input, want := range map[string]string{
		"PriceCheckout(cart Cart) Money":           "PriceCheckout",
		"func PriceCheckout(cart Cart) Money":      "PriceCheckout",
		"checkout.PriceCheckout(cart Cart) Money":  "PriceCheckout",
		"PriceCheckout cart fixture returns Money": "PriceCheckout",
	} {
		if got := dropInFunctionName(input); got != want {
			t.Fatalf("dropInFunctionName(%q) = %q, want %q", input, got, want)
		}
	}
}
