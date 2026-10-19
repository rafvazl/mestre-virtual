package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// scripted é um Provider de teste: emite chunks e, se gate != nil, espera
// um sinal antes de cada chunk após o primeiro (para provar que não há buffer).
type scripted struct {
	name     string
	chunks   []llm.Chunk
	startErr error
	genErr   error
	gate     chan struct{}
	infinite bool // emite até o ctx ser cancelado
}

func (s *scripted) Name() string { return s.name }

func (s *scripted) Generate(context.Context, llm.Request) (llm.Response, error) {
	if s.genErr != nil {
		return llm.Response{}, s.genErr
	}
	return llm.Response{Text: "resposta", Provider: s.name}, nil
}

func (s *scripted) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	if s.startErr != nil {
		return nil, s.startErr
	}
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		for i := 0; s.infinite || i < len(s.chunks); i++ {
			c := llm.Chunk{Text: "x"}
			if !s.infinite {
				c = s.chunks[i]
			}
			if i > 0 && s.gate != nil {
				select {
				case <-s.gate:
				case <-ctx.Done():
					return
				}
			}
			select {
			case ch <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// logBuf é um writer concorrente-seguro que guarda linhas JSON.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("linha de log não é JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func (l *logBuf) raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newJSON(t *testing.T, level slog.Level) (*slog.Logger, *logBuf) {
	t.Helper()
	buf := &logBuf{}
	lg, err := NewLogger(Options{Format: FormatJSON, Level: level, Writer: buf})
	if err != nil {
		t.Fatal(err)
	}
	return lg, buf
}

// waitGoroutines espera o número de goroutines voltar a no máximo base.
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

func drain(t *testing.T, ch <-chan llm.Chunk) []llm.Chunk {
	t.Helper()
	var got []llm.Chunk
	timeout := time.After(2 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, c)
		case <-timeout:
			t.Fatal("canal não fechou")
		}
	}
}

func TestNewLogger(t *testing.T) {
	var b bytes.Buffer
	lg, err := NewLogger(Options{Writer: &b, Level: slog.LevelWarn})
	if err != nil {
		t.Fatal(err)
	}
	lg.Info("some")
	lg.Warn("aparece", "k", 1)
	if out := b.String(); strings.Contains(out, "some") || !strings.Contains(out, "msg=aparece k=1") {
		t.Fatalf("saída text inesperada: %q", out)
	}
	if _, err := NewLogger(Options{Format: "xml"}); err == nil {
		t.Fatal("formato desconhecido deveria falhar")
	}
	lg, buf := newJSON(t, slog.LevelInfo)
	lg.Info("oi")
	if r := buf.records(t); len(r) != 1 || r[0]["msg"] != "oi" {
		t.Fatalf("json: %v", r)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{"": slog.LevelInfo, "DEBUG": slog.LevelDebug, " warn ": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError}
	for in, want := range cases {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Error("nível desconhecido deveria falhar")
	}
}

func TestGenerateLogsMetadataNotContent(t *testing.T) {
	lg, buf := newJSON(t, slog.LevelDebug)
	p := NewLoggingProvider(&scripted{name: "fake"}, lg)
	req := llm.Request{System: "sys-secreto", Prompt: "segredo do jogador", Tags: map[string]string{"kind": "npc"}}
	resp, err := p.Generate(context.Background(), req)
	if err != nil || resp.Text != "resposta" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if p.Name() != "fake" {
		t.Errorf("Name() = %q, decorator deve ser transparente", p.Name())
	}
	if strings.Contains(buf.raw(), "segredo") || strings.Contains(buf.raw(), "sys-secreto") {
		t.Fatalf("conteúdo do prompt vazou no log: %s", buf.raw())
	}
	r := buf.records(t)
	if len(r) != 1 {
		t.Fatalf("esperava 1 registro, veio %v", r)
	}
	rec := r[0]
	if rec["msg"] != "llm.done" || rec["level"] != "INFO" || rec["provider"] != "fake" || rec["op"] != "generate" ||
		rec["prompt_bytes"] != float64(len(req.Prompt)) || rec["response_bytes"] != float64(len("resposta")) ||
		rec["served_by"] != "fake" {
		t.Fatalf("registro inesperado: %v", rec)
	}
	if tags, _ := rec["tags"].(map[string]any); tags["kind"] != "npc" {
		t.Fatalf("tags: %v", rec["tags"])
	}
	if _, ok := rec["duration"]; !ok {
		t.Fatal("sem duration")
	}
}

func TestPromptLoggingOptIn(t *testing.T) {
	lg, buf := newJSON(t, slog.LevelDebug)
	p := NewLoggingProvider(&scripted{name: "fake"}, lg, WithPromptLogging())
	p.Generate(context.Background(), llm.Request{Prompt: "abro a porta"})
	r := buf.records(t)
	if len(r) != 2 || r[0]["msg"] != "llm.request" || r[0]["prompt"] != "abro a porta" || r[0]["level"] != "DEBUG" {
		t.Fatalf("registros: %v", r)
	}
}

func TestGenerateErrorLevels(t *testing.T) {
	for _, tc := range []struct {
		err   error
		level string
		msg   string
	}{
		{errors.New("boom"), "ERROR", "llm.error"},
		{context.Canceled, "WARN", "llm.canceled"},
		{context.DeadlineExceeded, "WARN", "llm.canceled"},
	} {
		lg, buf := newJSON(t, slog.LevelInfo)
		p := NewLoggingProvider(&scripted{name: "f", genErr: tc.err}, lg)
		if _, err := p.Generate(context.Background(), llm.Request{}); !errors.Is(err, tc.err) {
			t.Fatalf("erro deve ser repassado intacto: %v", err)
		}
		r := buf.records(t)
		if len(r) != 1 || r[0]["level"] != tc.level || r[0]["msg"] != tc.msg || r[0]["error"] != tc.err.Error() {
			t.Fatalf("%v: %v", tc.err, r)
		}
	}
}

func TestStreamPassthroughAndMetrics(t *testing.T) {
	lg, buf := newJSON(t, slog.LevelInfo)
	inner := &scripted{name: "fake", chunks: []llm.Chunk{{Text: "ab"}, {Text: "cde"}, {Err: errors.New("caiu")}}}
	p := NewLoggingProvider(inner, lg)
	ch, err := p.Stream(context.Background(), llm.Request{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(t, ch)
	if len(got) != 3 || got[0].Text != "ab" || got[1].Text != "cde" || got[2].Err == nil {
		t.Fatalf("chunks alterados: %+v", got)
	}
	r := buf.records(t)
	if len(r) != 1 {
		t.Fatalf("registros: %v", r)
	}
	rec := r[0]
	if rec["msg"] != "llm.error" || rec["chunks"] != float64(2) || rec["response_bytes"] != float64(5) ||
		rec["started"] != true || rec["error"] != "caiu" {
		t.Fatalf("registro: %v", rec)
	}
	if _, ok := rec["first_chunk"]; !ok {
		t.Fatal("sem first_chunk")
	}
}

func TestStreamStartError(t *testing.T) {
	lg, buf := newJSON(t, slog.LevelInfo)
	p := NewLoggingProvider(&scripted{name: "f", startErr: llm.ErrNotConfigured}, lg)
	ch, err := p.Stream(context.Background(), llm.Request{})
	if ch != nil || !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("ch %v err %v", ch, err)
	}
	if r := buf.records(t); len(r) != 1 || r[0]["started"] != false || r[0]["level"] != "ERROR" {
		t.Fatalf("registros: %v", r)
	}
}

// O decorator não pode bufferizar: o primeiro chunk precisa chegar ao
// consumidor antes de o provider produzir o segundo.
func TestStreamDoesNotBuffer(t *testing.T) {
	gate := make(chan struct{})
	inner := &scripted{name: "f", chunks: []llm.Chunk{{Text: "1"}, {Text: "2"}}, gate: gate}
	ch, err := NewLoggingProvider(inner, nil).Stream(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-ch:
		if c.Text != "1" {
			t.Fatalf("primeiro chunk = %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("primeiro chunk retido pelo decorator")
	}
	close(gate)
	if rest := drain(t, ch); len(rest) != 1 || rest[0].Text != "2" {
		t.Fatalf("resto = %+v", rest)
	}
}

func TestStreamCancelClosesAndLogs(t *testing.T) {
	base := runtime.NumGoroutine()
	lg, buf := newJSON(t, slog.LevelInfo)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := NewLoggingProvider(&scripted{name: "f", infinite: true}, lg).Stream(ctx, llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	<-ch
	cancel()
	drain(t, ch) // precisa fechar
	waitGoroutines(t, base)
	r := buf.records(t)
	if len(r) != 1 || r[0]["msg"] != "llm.canceled" || r[0]["level"] != "WARN" {
		t.Fatalf("registros: %v", r)
	}
}

// Consumidor que cancela e para de ler: nenhuma goroutine pode ficar presa.
func TestStreamCancelWithoutReading(t *testing.T) {
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	_, err := NewLoggingProvider(&scripted{name: "f", infinite: true}, nil).Stream(ctx, llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitGoroutines(t, base)
}

func TestFixedClockDuration(t *testing.T) {
	lg, buf := newJSON(t, slog.LevelInfo)
	t0 := time.Unix(0, 0)
	calls := 0
	clock := func() time.Time { calls++; return t0.Add(time.Duration(calls-1) * 250 * time.Millisecond) }
	NewLoggingProvider(&scripted{name: "f"}, lg, withClock(clock)).Generate(context.Background(), llm.Request{})
	// slog JSON serializa Duration em nanossegundos.
	if r := buf.records(t); r[0]["duration"] != float64(250*time.Millisecond) {
		t.Fatalf("duration = %v", r[0]["duration"])
	}
}
