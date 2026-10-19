package obs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// LoggingProvider é um decorator de llm.Provider que registra uma linha de
// log por chamada: provider, operação, duração, tamanho de prompt/resposta,
// número de chunks e erro.
//
// Por padrão o CONTEÚDO do prompt não é logado (pode conter dados do
// jogador e infla o log); só tamanhos. WithPromptLogging habilita.
//
// Em Stream, cada chunk é repassado assim que chega (canal sem buffer, nada
// é acumulado) e o cancelamento do contexto é respeitado: o decorator não
// muda o que o consumidor observa, só observa junto.
type LoggingProvider struct {
	inner     llm.Provider
	log       *slog.Logger
	logPrompt bool
	now       func() time.Time
}

// LoggingOption configura um LoggingProvider.
type LoggingOption func(*LoggingProvider)

// WithPromptLogging inclui System e Prompt no log, em nível Debug. Use só
// em desenvolvimento.
func WithPromptLogging() LoggingOption { return func(p *LoggingProvider) { p.logPrompt = true } }

// withClock troca o relógio (testes).
func withClock(now func() time.Time) LoggingOption { return func(p *LoggingProvider) { p.now = now } }

var _ llm.Provider = (*LoggingProvider)(nil)

// NewLoggingProvider envolve inner. log nil = Discard().
func NewLoggingProvider(inner llm.Provider, log *slog.Logger, opts ...LoggingOption) *LoggingProvider {
	if log == nil {
		log = Discard()
	}
	p := &LoggingProvider{inner: inner, log: log, now: time.Now}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name implementa llm.Provider; é transparente (devolve o nome do envolvido).
func (p *LoggingProvider) Name() string { return p.inner.Name() }

// Unwrap devolve o provider envolvido.
func (p *LoggingProvider) Unwrap() llm.Provider { return p.inner }

// Generate implementa llm.Provider.
func (p *LoggingProvider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	start := p.now()
	p.logRequest(ctx, "generate", req)
	resp, err := p.inner.Generate(ctx, req)
	attrs := append(p.baseAttrs("generate", req),
		slog.Duration("duration", p.now().Sub(start)),
		slog.Int("response_bytes", len(resp.Text)),
	)
	if resp.Provider != "" {
		attrs = append(attrs, slog.String("served_by", resp.Provider))
	}
	p.finish(ctx, err, attrs)
	return resp, err
}

// Stream implementa llm.Provider.
func (p *LoggingProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	start := p.now()
	p.logRequest(ctx, "stream", req)
	src, err := p.inner.Stream(ctx, req)
	if err != nil {
		p.finish(ctx, err, append(p.baseAttrs("stream", req),
			slog.Duration("duration", p.now().Sub(start)),
			slog.Bool("started", false),
		))
		return nil, err
	}

	out := make(chan llm.Chunk)
	go func() {
		defer close(out)
		var (
			chunks, bytes int
			first         time.Duration = -1
			streamErr     error
			canceled      bool
		)
	loop:
		for c := range src {
			if c.Err != nil {
				streamErr = c.Err
			} else {
				chunks++
				bytes += len(c.Text)
				if first < 0 {
					first = p.now().Sub(start)
				}
			}
			select {
			case out <- c:
			case <-ctx.Done():
				// Consumidor desistiu: paramos de repassar. O provider envolvido
				// também observa ctx e fecha src por conta própria.
				canceled = true
				break loop
			}
		}
		// Um stream que terminou "limpo" com ctx cancelado foi truncado:
		// registra como cancelado para não parecer sucesso no log.
		if streamErr == nil && (canceled || ctx.Err() != nil) {
			streamErr = ctx.Err()
		}
		attrs := append(p.baseAttrs("stream", req),
			slog.Duration("duration", p.now().Sub(start)),
			slog.Bool("started", true),
			slog.Int("chunks", chunks),
			slog.Int("response_bytes", bytes),
		)
		if first >= 0 {
			attrs = append(attrs, slog.Duration("first_chunk", first))
		}
		p.finish(ctx, streamErr, attrs)
	}()
	return out, nil
}

func (p *LoggingProvider) baseAttrs(op string, req llm.Request) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("provider", p.inner.Name()),
		slog.String("op", op),
		slog.Int("prompt_bytes", len(req.Prompt)),
		slog.Int("system_bytes", len(req.System)),
	}
	if len(req.Tags) > 0 {
		tags := make([]any, 0, len(req.Tags))
		for k, v := range req.Tags {
			tags = append(tags, slog.String(k, v))
		}
		attrs = append(attrs, slog.Group("tags", tags...))
	}
	return attrs
}

// logRequest registra o conteúdo da requisição, só se habilitado.
func (p *LoggingProvider) logRequest(ctx context.Context, op string, req llm.Request) {
	if !p.logPrompt {
		return
	}
	p.log.LogAttrs(ctx, slog.LevelDebug, "llm.request",
		slog.String("provider", p.inner.Name()),
		slog.String("op", op),
		slog.String("system", req.System),
		slog.String("prompt", req.Prompt),
	)
}

// finish registra o resultado: Info no sucesso, Warn em cancelamento/prazo
// (decisão de quem chamou, não falha do backend), Error no resto.
func (p *LoggingProvider) finish(ctx context.Context, err error, attrs []slog.Attr) {
	level, msg := slog.LevelInfo, "llm.done"
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		level, msg = slog.LevelWarn, "llm.canceled"
		attrs = append(attrs, slog.String("error", err.Error()))
	default:
		level, msg = slog.LevelError, "llm.error"
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	// context.WithoutCancel: o registro final deve sair mesmo com ctx cancelado.
	p.log.LogAttrs(context.WithoutCancel(ctx), level, msg, attrs...)
}
