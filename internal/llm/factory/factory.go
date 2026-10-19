// Package factory monta um llm.Provider a partir de configuração.
//
// Fica fora de internal/llm porque importa os adapters (local, remote), que
// por sua vez importam internal/llm — mantê-lo lá criaria ciclo de import.
package factory

import (
	"fmt"

	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/llm/local"
	"github.com/rafvazl/mestre-virtual/internal/llm/remote"
)

// Config descreve qual backend usar.
type Config struct {
	// Backend: "local", "remote" ou "routing".
	Backend string
	// RemoteModel é o modelo usado pelo adapter remoto.
	RemoteModel string
}

// New cria o Provider descrito por cfg.
func New(cfg Config) (llm.Provider, error) {
	switch cfg.Backend {
	case "", "local":
		return local.NewEchoProvider(), nil
	case "remote":
		return remote.NewStubProvider(cfg.RemoteModel), nil
	case "routing":
		// Remoto como padrão, local como fallback: enquanto o remoto é stub,
		// tudo cai no local, exercitando o caminho de fallback.
		return &llm.RoutingProvider{
			Default:  remote.NewStubProvider(cfg.RemoteModel),
			Fallback: local.NewEchoProvider(),
		}, nil
	default:
		return nil, fmt.Errorf("factory: backend desconhecido %q (use local, remote ou routing)", cfg.Backend)
	}
}
