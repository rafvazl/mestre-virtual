package factory

import (
	"context"
	"errors"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

func TestNew(t *testing.T) {
	for _, b := range []string{"", "local", "remote", "routing"} {
		if _, err := New(Config{Backend: b}); err != nil {
			t.Errorf("%q: %v", b, err)
		}
	}
	if _, err := New(Config{Backend: "x"}); err == nil {
		t.Error("esperava erro para backend desconhecido")
	}
}

func TestRemoteStubFailsExplicitly(t *testing.T) {
	p, _ := New(Config{Backend: "remote", RemoteModel: "m"})
	if _, err := p.Generate(context.Background(), llm.Request{}); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestRoutingFallsBackToLocal(t *testing.T) {
	p, _ := New(Config{Backend: "routing"})
	resp, err := p.Generate(context.Background(), llm.Request{Prompt: "oi"})
	if err != nil || resp.Provider != "local/echo" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}
