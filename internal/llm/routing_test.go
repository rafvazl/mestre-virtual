package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fake é um Provider configurável para testes.
type fake struct {
	name     string
	genErr   error
	startErr error
	chunks   []Chunk
	calls    int
}

func (f *fake) Name() string { return f.name }
func (f *fake) Generate(context.Context, Request) (Response, error) {
	f.calls++
	if f.genErr != nil {
		return Response{}, f.genErr
	}
	return Response{Text: f.name, Provider: f.name}, nil
}
func (f *fake) Stream(context.Context, Request) (<-chan Chunk, error) {
	f.calls++
	if f.startErr != nil {
		return nil, f.startErr
	}
	ch := make(chan Chunk, len(f.chunks))
	for _, c := range f.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func collect(t *testing.T, ch <-chan Chunk) (string, error) {
	t.Helper()
	var b strings.Builder
	var err error
	for c := range ch {
		if c.Err != nil {
			err = c.Err
			continue
		}
		b.WriteString(c.Text)
	}
	return b.String(), err
}

func TestRouteByRule(t *testing.T) {
	loc, rem := &fake{name: "local"}, &fake{name: "remote"}
	r := &RoutingProvider{
		Rules:   []Rule{{Name: "npc", Match: func(q Request) bool { return q.Tags["kind"] == "npc" }, Provider: loc}},
		Default: rem,
	}
	resp, _ := r.Generate(context.Background(), Request{Tags: map[string]string{"kind": "npc"}})
	if resp.Provider != "local" {
		t.Fatalf("got %s", resp.Provider)
	}
	resp, _ = r.Generate(context.Background(), Request{})
	if resp.Provider != "remote" {
		t.Fatalf("got %s", resp.Provider)
	}
}

func TestNoRoute(t *testing.T) {
	_, err := (&RoutingProvider{}).Generate(context.Background(), Request{})
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateFallback(t *testing.T) {
	r := &RoutingProvider{Default: &fake{name: "a", genErr: errors.New("x")}, Fallback: &fake{name: "b"}}
	resp, err := r.Generate(context.Background(), Request{})
	if err != nil || resp.Provider != "b" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func TestStreamFallbackOnStartError(t *testing.T) {
	r := &RoutingProvider{
		Default:  &fake{name: "a", startErr: errors.New("down")},
		Fallback: &fake{name: "b", chunks: []Chunk{{Text: "ok"}}},
	}
	ch, err := r.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if txt, err := collect(t, ch); txt != "ok" || err != nil {
		t.Fatalf("txt %q err %v", txt, err)
	}
}

func TestStreamFallbackBeforeFirstChunk(t *testing.T) {
	fb := &fake{name: "b", chunks: []Chunk{{Text: "B"}}}
	r := &RoutingProvider{
		Default:  &fake{name: "a", chunks: []Chunk{{Err: errors.New("boom")}}},
		Fallback: fb,
	}
	ch, _ := r.Stream(context.Background(), Request{})
	if txt, err := collect(t, ch); txt != "B" || err != nil {
		t.Fatalf("txt %q err %v", txt, err)
	}
}

func TestStreamNoSwitchAfterFirstChunk(t *testing.T) {
	fb := &fake{name: "b", chunks: []Chunk{{Text: "B"}}}
	r := &RoutingProvider{
		Default:  &fake{name: "a", chunks: []Chunk{{Text: "A1 "}, {Err: errors.New("boom")}}},
		Fallback: fb,
	}
	ch, _ := r.Stream(context.Background(), Request{})
	txt, err := collect(t, ch)
	if txt != "A1 " || err == nil {
		t.Fatalf("txt %q err %v", txt, err)
	}
	if fb.calls != 0 {
		t.Fatal("fallback não deveria ser chamado após o primeiro chunk")
	}
}
