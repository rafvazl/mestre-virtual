// Package obs reúne a observabilidade do mestre virtual: construção do
// *slog.Logger (stdlib, sem dependências) e decorators que instrumentam
// componentes sem alterar seu comportamento.
//
// Nada aqui liga logging sozinho: quem monta a aplicação (a CLI) decide se
// cria um Logger e se envolve o llm.Provider com NewLoggingProvider.
package obs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format é o formato de saída do log.
type Format string

// Formatos suportados.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Options configura NewLogger. O valor zero produz log em texto, nível Info,
// em os.Stderr.
type Options struct {
	// Level é o nível mínimo registrado.
	Level slog.Level
	// Format é "text" (padrão) ou "json".
	Format Format
	// Writer recebe o log; nil = os.Stderr (stdout fica livre para a narração).
	Writer io.Writer
	// AddSource inclui arquivo:linha de quem logou.
	AddSource bool
}

// NewLogger cria um *slog.Logger a partir de opts.
func NewLogger(opts Options) (*slog.Logger, error) {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	ho := &slog.HandlerOptions{Level: opts.Level, AddSource: opts.AddSource}
	switch opts.Format {
	case "", FormatText:
		return slog.New(slog.NewTextHandler(w, ho)), nil
	case FormatJSON:
		return slog.New(slog.NewJSONHandler(w, ho)), nil
	default:
		return nil, fmt.Errorf("obs: formato de log desconhecido %q (use text ou json)", opts.Format)
	}
}

// ParseLevel converte "debug", "info", "warn"/"warning" ou "error"
// (sem diferenciar maiúsculas) em slog.Level. Vazio = Info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("obs: nível de log desconhecido %q (use debug, info, warn ou error)", s)
}

// Discard devolve um Logger que descarta tudo (útil como padrão e em testes).
func Discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
