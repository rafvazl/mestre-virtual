package local

import (
	"context"
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

func TestStreamMatchesGenerate(t *testing.T) {
	p := NewEchoProvider()
	req := llm.Request{Prompt: "abro a porta"}
	full, _ := p.Generate(context.Background(), req)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for c := range ch {
		b.WriteString(c.Text)
	}
	if b.String() != full.Text {
		t.Fatalf("stream %q != generate %q", b.String(), full.Text)
	}
	parsed, errs := llm.ParseResponse(full.Text)
	if !strings.Contains(parsed.Narrative, "abro a porta") || len(errs) != 0 {
		t.Fatalf("parsed %+v errs %v", parsed, errs)
	}
}

func TestEchoUsesLastLine(t *testing.T) {
	p := NewEchoProvider()
	resp, _ := p.Generate(context.Background(), llm.Request{Prompt: "## Contexto\nmuita coisa\n\nAria (aria): abro a porta\n"})
	parsed, _ := llm.ParseResponse(resp.Text)
	if parsed.Narrative != "O mestre ouve: Aria (aria): abro a porta" {
		t.Fatalf("narrativa %q", parsed.Narrative)
	}
}
