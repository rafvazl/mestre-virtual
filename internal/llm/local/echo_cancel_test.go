package local

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

var _ llm.Provider = (*EchoProvider)(nil)

func waitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines vazando: %d > %d", runtime.NumGoroutine(), base)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEchoCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewEchoProvider()
	if _, err := p.Generate(ctx, llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate err = %v", err)
	}
	if ch, err := p.Stream(ctx, llm.Request{}); ch != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream ch %v err %v", ch, err)
	}
}

func TestEchoStreamCancelMidStream(t *testing.T) {
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := NewEchoProvider().Stream(ctx, llm.Request{Prompt: strings.Repeat("palavra ", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if c := <-ch; c.Text == "" || c.Err != nil {
		t.Fatalf("primeiro chunk = %+v", c)
	}
	cancel()
	// Depois do cancelamento o canal precisa fechar; chunks de erro, se
	// vierem, são context.Canceled.
	timeout := time.After(2 * time.Second)
	n := 0
	for done := false; !done; {
		select {
		case c, ok := <-ch:
			if !ok {
				done = true
				break
			}
			if c.Err != nil && !errors.Is(c.Err, context.Canceled) {
				t.Fatalf("erro inesperado: %v", c.Err)
			}
			n++
		case <-timeout:
			t.Fatal("canal não fechou após cancelamento")
		}
	}
	if n >= 100 {
		t.Fatalf("stream não parou após cancelamento (%d chunks)", n)
	}
	waitGoroutines(t, base)
}

// Consumidor que cancela e abandona o canal: a goroutine do provider não
// pode ficar bloqueada no envio.
func TestEchoStreamCancelWithoutReading(t *testing.T) {
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := NewEchoProvider().Stream(ctx, llm.Request{Prompt: "a b c d e"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitGoroutines(t, base)
}

func TestEchoDeterministic(t *testing.T) {
	p := NewEchoProvider()
	a, _ := p.Generate(context.Background(), llm.Request{Prompt: "  x  "})
	b, _ := p.Generate(context.Background(), llm.Request{Prompt: "x"})
	if a != b || a.Provider != "local/echo" {
		t.Fatalf("%+v != %+v", a, b)
	}
}
