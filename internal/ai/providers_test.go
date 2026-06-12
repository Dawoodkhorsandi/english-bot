package ai

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a configurable Provider stub for chain tests.
type fakeProvider struct {
	name    string
	enabled bool
	text    string
	err     error
	calls   int
}

func (p *fakeProvider) Name() string  { return p.name }
func (p *fakeProvider) Enabled() bool { return p.enabled }
func (p *fakeProvider) Generate(ctx context.Context, prompt string) (string, error) {
	p.calls++
	return p.text, p.err
}

func TestChainHasAnyAndNames(t *testing.T) {
	if NewChain().HasAny() {
		t.Error("empty chain should report HasAny=false")
	}
	c := NewChain(&fakeProvider{name: "a"}, &fakeProvider{name: "b"})
	if !c.HasAny() {
		t.Error("non-empty chain should report HasAny=true")
	}
	names := c.ProviderNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("ProviderNames = %v, want [a b]", names)
	}
}

func TestChainGenerateNoProviders(t *testing.T) {
	if _, _, err := NewChain().Generate(context.Background(), "hi"); err == nil {
		t.Error("expected error from empty chain")
	}
}

func TestChainGenerateFirstSuccess(t *testing.T) {
	p1 := &fakeProvider{name: "p1", text: "winner"}
	p2 := &fakeProvider{name: "p2", text: "loser"}
	text, provider, err := NewChain(p1, p2).Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "winner" || provider != "p1" {
		t.Errorf("got (%q,%q), want (winner,p1)", text, provider)
	}
	if p2.calls != 0 {
		t.Errorf("p2 should not have been called, got %d calls", p2.calls)
	}
}

func TestChainGenerateFailover(t *testing.T) {
	p1 := &fakeProvider{name: "p1", err: errors.New("boom")}
	p2 := &fakeProvider{name: "p2", text: "recovered"}
	text, provider, err := NewChain(p1, p2).Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error after failover: %v", err)
	}
	if text != "recovered" || provider != "p2" {
		t.Errorf("got (%q,%q), want (recovered,p2)", text, provider)
	}
}

func TestChainGenerateAllFail(t *testing.T) {
	p1 := &fakeProvider{name: "p1", err: errors.New("boom1")}
	p2 := &fakeProvider{name: "p2", err: errors.New("boom2")}
	if _, _, err := NewChain(p1, p2).Generate(context.Background(), "prompt"); err == nil {
		t.Error("expected error when all providers fail")
	}
}

func TestChainGenerateRateLimitFailsOver(t *testing.T) {
	// A 429 should fail over immediately without exhausting the retry budget.
	p1 := &fakeProvider{name: "p1", err: &providerHTTPError{status: 429, body: "slow down"}}
	p2 := &fakeProvider{name: "p2", text: "ok"}
	text, _, err := NewChain(p1, p2).Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "ok" {
		t.Errorf("got %q, want ok", text)
	}
	if p1.calls != 1 {
		t.Errorf("rate-limited provider should be tried once, got %d", p1.calls)
	}
}
