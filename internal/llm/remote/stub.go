// Package remote contém adapters para LLMs remotos (APIs HTTP).
//
// StubProvider é um placeholder que sempre falha com erro explícito: o
// sandbox onde o projeto nasceu não tinha acesso a proxy.golang.org, então
// nenhum SDK real foi adicionado. Trocar por um adapter real (Anthropic,
// OpenAI etc.) não exige mudança no núcleo.
package remote

import (
	"context"
	"fmt"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// StubProvider sempre retorna llm.ErrNotConfigured.
type StubProvider struct {
	Model string
}

// NewStubProvider cria um StubProvider para o modelo informado.
func NewStubProvider(model string) *StubProvider { return &StubProvider{Model: model} }

// Name implementa llm.Provider.
func (p *StubProvider) Name() string { return "remote/stub:" + p.Model }

// Generate implementa llm.Provider.
func (p *StubProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, p.err()
}

// Stream implementa llm.Provider.
func (p *StubProvider) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, p.err()
}

func (p *StubProvider) err() error {
	return fmt.Errorf("%s: adapter remoto ainda não implementado: %w", p.Name(), llm.ErrNotConfigured)
}
