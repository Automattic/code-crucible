package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoCallableSignaturesIgnoreParameterNames(t *testing.T) {
	dir := t.TempDir()
	source := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart) (total Money, err error) { return 0, nil }

type Pricer struct{}

func (p *Pricer) Price(cart Cart) Money { return 0 }
`
	if err := os.WriteFile(filepath.Join(dir, "pricing.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	signatures, err := goCallableSignatures(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "func(Cart) (Money, error)"
	if signatures["PriceCheckout"].Signature != want {
		t.Fatalf("PriceCheckout signature = %q, want %q", signatures["PriceCheckout"].Signature, want)
	}
	wantMethod := "method *Pricer func(Cart) Money"
	if signatures["Pricer.Price"].Signature != wantMethod {
		t.Fatalf("Pricer.Price signature = %q, want %q", signatures["Pricer.Price"].Signature, wantMethod)
	}
}

func TestSemanticContractErrorsRequireExportedGoCallables(t *testing.T) {
	root := t.TempDir()
	baselineSrc := filepath.Join(root, "baseline")
	candidateSrc := filepath.Join(root, "candidate")
	if err := os.MkdirAll(baselineSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(candidateSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	baseline := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart) Money { return 0 }

func helper(cart Cart) Money { return 0 }
`
	candidate := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart, discount int) Money { return 0 }
`
	if err := os.WriteFile(filepath.Join(baselineSrc, "pricing.go"), []byte(baseline), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateSrc, "pricing.go"), []byte(candidate), 0o644); err != nil {
		t.Fatal(err)
	}

	errors := semanticContractErrors(root, baselineSrc, candidateSrc, map[string]bool{".go": true}, map[string]bool{".go": true})
	if len(errors) != 1 {
		t.Fatalf("errors = %#v, want one exported signature error", errors)
	}
	if errors[0] != "candidate contract failed: exported Go callable PriceCheckout signature changed: got func(Cart, int) Money, want func(Cart) Money" {
		t.Fatalf("error = %q", errors[0])
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
