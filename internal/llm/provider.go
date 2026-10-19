// Package llm define o contrato de geração de texto usado pelo mestre virtual.
//
// O núcleo do jogo (estado, regras, memória) nunca depende de um backend
// específico: ele fala apenas com a interface Provider. Adapters concretos
// vivem em subpacotes (local, remote) e são montados por internal/llm/factory,
// que fica fora deste pacote para evitar ciclo de import.
package llm

import (
	"context"
	"errors"
)

// Request é uma requisição de geração.
type Request struct {
	// System é a instrução de sistema (papel do mestre, contrato de saída).
	System string
	// Prompt é o conteúdo do turno (contexto + ação do jogador).
	Prompt string
	// MaxTokens limita o tamanho da resposta; 0 = padrão do provider.
	MaxTokens int
	// Temperature controla aleatoriedade; 0 = padrão do provider.
	Temperature float64
	// Tags permite que regras de roteamento decidam o backend
	// (ex.: "kind" => "npc_dialogue").
	Tags map[string]string
}

// Response é o resultado de uma geração completa.
type Response struct {
	Text     string
	Provider string
}

// Chunk é um pedaço de uma geração em streaming. Se Err != nil, o stream
// terminou com erro e nenhum outro chunk será enviado.
type Chunk struct {
	Text string
	Err  error
}

// Provider é a abstração de qualquer backend de LLM.
type Provider interface {
	// Name identifica o provider (para logs e roteamento).
	Name() string
	// Generate produz a resposta completa.
	Generate(ctx context.Context, req Request) (Response, error)
	// Stream produz a resposta em chunks. O canal é fechado ao final.
	// Um erro retornado diretamente significa que o stream nem começou.
	//
	// Contrato de cancelamento:
	//   - Provider: ao ver ctx cancelado, para de produzir e fecha o canal
	//     logo. Pode (não precisa) enviar antes um Chunk com Err = ctx.Err(),
	//     sem bloquear se ninguém estiver lendo.
	//   - Consumidor: drena o canal até o fechamento (para não prender a
	//     goroutine do provider) e, ao final, checa ctx.Err(). Um stream que
	//     fechou "limpo" com ctx cancelado está truncado: deve ser tratado
	//     como falha de geração e nada do que chegou pode ser aplicado.
	//     game.Engine segue esse contrato.
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// Erros sentinela compartilhados pelos adapters.
var (
	ErrNotConfigured = errors.New("llm: provider não configurado")
	ErrNoRoute       = errors.New("llm: nenhuma rota disponível")
)
