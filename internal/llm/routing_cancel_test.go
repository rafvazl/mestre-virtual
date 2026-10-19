package llm

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// endless emite chunks até o contexto ser cancelado (como um backend real
// bem-comportado) e conta goroutines vivas.
type endless struct {
	name  string
	live  atomic.Int32
	calls atomic.Int32
}

func (e *endless) Name() string { return e.name }
func (e *endless) Generate(context.Context, Request) (Response, error) {
	return Response{}, errors.New("não usado")
}
func (e *endless) Stream(ctx context.Context, _ Request) (<-chan Chunk, error) {
	e.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan Chunk)
	e.live.Add(1)
	go func() {
		defer e.live.Add(-1)
		defer close(ch)
		for {
			select {
			case ch <- Chunk{Text: "x"}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func waitClosed(t *testing.T, ch <-chan Chunk) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("canal de saída não fechou após cancelamento")
		}
	}
}

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

func TestRoutingStreamCancelMidStream(t *testing.T) {
	base := runtime.NumGoroutine()
	primary := &endless{name: "a"}
	// Com Fallback, o RoutingProvider cria sua própria goroutine de repasse.
	r := &RoutingProvider{Default: primary, Fallback: &fake{name: "b"}}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := r.Stream(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	<-ch
	cancel()
	waitClosed(t, ch)
	waitGoroutines(t, base)
	if primary.live.Load() != 0 {
		t.Fatal("goroutine do primário ainda viva")
	}
}

func TestRoutingStreamCancelWithoutReading(t *testing.T) {
	base := runtime.NumGoroutine()
	primary := &endless{name: "a"}
	r := &RoutingProvider{Default: primary, Fallback: &fake{name: "b"}}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := r.Stream(ctx, Request{}); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitGoroutines(t, base)
}

func TestRoutingStreamCancelDuringFallback(t *testing.T) {
	base := runtime.NumGoroutine()
	fb := &endless{name: "b"}
	r := &RoutingProvider{Default: &fake{name: "a", chunks: []Chunk{{Err: errors.New("boom")}}}, Fallback: fb}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := r.Stream(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if c := <-ch; c.Text != "x" {
		t.Fatalf("esperava chunk do fallback, veio %+v", c)
	}
	cancel()
	waitClosed(t, ch)
	waitGoroutines(t, base)
}

// Regressão: com ctx já cancelado, o erro de início do primário não deve
// disparar o fallback (Generate já se comportava assim; Stream não).
func TestRoutingStreamNoFallbackWhenCanceled(t *testing.T) {
	fb := &endless{name: "b"}
	r := &RoutingProvider{Default: &endless{name: "a"}, Fallback: fb}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch, err := r.Stream(ctx, Request{})
	if ch != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ch %v err %v", ch, err)
	}
	if fb.calls.Load() != 0 {
		t.Fatal("fallback chamado com contexto cancelado")
	}
}

func TestRoutingGenerateNoFallbackWhenCanceled(t *testing.T) {
	fb := &fake{name: "b"}
	r := &RoutingProvider{Default: &fake{name: "a", genErr: context.Canceled}, Fallback: fb}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Generate(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if fb.calls != 0 {
		t.Fatal("fallback chamado com contexto cancelado")
	}
}

func TestRoutingStreamWithoutFallbackReturnsSource(t *testing.T) {
	primary := &fake{name: "a", chunks: []Chunk{{Text: "A"}}}
	ch, err := (&RoutingProvider{Default: primary}).Stream(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if txt, err := collect(t, ch); txt != "A" || err != nil {
		t.Fatalf("txt %q err %v", txt, err)
	}
}

func TestRoutingBothFail(t *testing.T) {
	r := &RoutingProvider{
		Default:  &fake{name: "a", chunks: []Chunk{{Err: errors.New("boom")}}},
		Fallback: &fake{name: "b", startErr: ErrNotConfigured},
	}
	ch, _ := r.Stream(context.Background(), Request{})
	if _, err := collect(t, ch); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	r.Default = &fake{name: "a", genErr: errors.New("x")}
	r.Fallback = &fake{name: "b", genErr: ErrNotConfigured}
	if _, err := r.Generate(context.Background(), Request{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestFallbackSameAsPrimaryIsIgnored(t *testing.T) {
	a := &fake{name: "a", genErr: errors.New("x")}
	r := &RoutingProvider{Default: a, Fallback: a}
	if _, err := r.Generate(context.Background(), Request{}); err == nil || a.calls != 1 {
		t.Fatalf("err %v calls %d", err, a.calls)
	}
}

func TestRuleWithNilMatchOrProviderIsSkipped(t *testing.T) {
	def := &fake{name: "def"}
	r := &RoutingProvider{
		Rules:   []Rule{{Name: "sem-match", Provider: &fake{name: "x"}}, {Name: "sem-provider", Match: func(Request) bool { return true }}},
		Default: def,
	}
	if p, err := r.Route(Request{}); err != nil || p != def {
		t.Fatalf("p %v err %v", p, err)
	}
}
