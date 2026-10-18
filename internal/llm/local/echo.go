// Package local contém adapters para LLMs executados localmente.
//
// Hoje só existe EchoProvider, um placeholder determinístico que permite
// exercitar o loop de jogo sem modelo real. Um adapter para Ollama/llama.cpp
// entra aqui sem tocar no núcleo.
package local

import (
	"context"
	"fmt"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// EchoProvider devolve uma resposta no contrato [NARRATIVE]/[MECHANICS]
// ecoando o prompt. Útil para testes e para rodar a CLI offline.
type EchoProvider struct{}

// NewEchoProvider cria um EchoProvider.
func NewEchoProvider() *EchoProvider { return &EchoProvider{} }

// Name implementa llm.Provider.
func (p *EchoProvider) Name() string { return "local/echo" }

// Generate implementa llm.Provider.
func (p *EchoProvider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: p.render(req), Provider: p.Name()}, nil
}

// Stream implementa llm.Provider, emitindo uma palavra por chunk.
func (p *EchoProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	words := strings.SplitAfter(p.render(req), " ")
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		for _, w := range words {
			select {
			case <-ctx.Done():
				// Envio não bloqueante: o consumidor pode já ter desistido.
				select {
				case ch <- llm.Chunk{Err: ctx.Err()}:
				default:
				}
				return
			case ch <- llm.Chunk{Text: w}:
			}
		}
	}()
	return ch, nil
}

func (p *EchoProvider) render(req llm.Request) string {
	return fmt.Sprintf("[NARRATIVE]\nO mestre ouve: %s\n[MECHANICS]\n", strings.TrimSpace(req.Prompt))
}
