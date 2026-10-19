package remote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

var _ llm.Provider = (*StubProvider)(nil)

func TestStubName(t *testing.T) {
	if got := NewStubProvider("claude-x").Name(); got != "remote/stub:claude-x" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestStubGenerateFailsExplicitly(t *testing.T) {
	p := NewStubProvider("m")
	resp, err := p.Generate(context.Background(), llm.Request{Prompt: "oi"})
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if resp != (llm.Response{}) {
		t.Fatalf("resposta deveria ser vazia: %+v", resp)
	}
	if !strings.Contains(err.Error(), "remote/stub:m") {
		t.Fatalf("erro deveria identificar o provider: %v", err)
	}
}

func TestStubStreamFailsBeforeStarting(t *testing.T) {
	ch, err := NewStubProvider("m").Stream(context.Background(), llm.Request{})
	if ch != nil || !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("ch %v err %v", ch, err)
	}
}

// Com o stub como primário, o roteamento deve cair no fallback nos dois modos.
func TestStubTriggersRoutingFallback(t *testing.T) {
	r := &llm.RoutingProvider{Default: NewStubProvider("m"), Fallback: okProvider{}}
	if resp, err := r.Generate(context.Background(), llm.Request{}); err != nil || resp.Provider != "ok" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	ch, err := r.Stream(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for c := range ch {
		got += c.Text
	}
	if got != "ok" {
		t.Fatalf("stream = %q", got)
	}
}

type okProvider struct{}

func (okProvider) Name() string { return "ok" }
func (okProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: "ok", Provider: "ok"}, nil
}
func (okProvider) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	ch <- llm.Chunk{Text: "ok"}
	close(ch)
	return ch, nil
}
