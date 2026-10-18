// Package memory é a memória de aventura do mestre virtual.
//
// Ela guarda o que aconteceu (eventos), o que se sabe (fatos) e resumos de
// sessões antigas, e devolve ao LLM só o trecho relevante e limitado para o
// turno atual — "quem é o velho guardião", "o que o grupo prometeu", "onde a
// espada foi perdida" — sem estourar a janela de contexto.
//
// Peças:
//
//	Store          memória por campanha, em RAM + JSON atômico em data/memory/
//	Recall         ranking lexical (BM25-lite) + Refs + recência + importância
//	Embedder       opcional: similaridade vetorial combinada ao lexical
//	Compactor      troca entradas antigas por um resumo (Summarizer)
//	ContextBuilder monta o bloco de memória do prompt dentro de um orçamento
//	EventRecorder  registra eventos do events.Dispatcher automaticamente
//
// Tudo é determinístico quando não há LLM/Embedder: mesma memória e mesma
// consulta produzem sempre o mesmo resultado, na mesma ordem.
package memory

import (
	"context"
	"errors"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

// Kind classifica uma entrada de memória.
type Kind string

const (
	// KindFact é algo que passa a ser verdade no mundo ("o guardião se
	// chama Odran", "o grupo prometeu devolver o amuleto").
	KindFact Kind = "fact"
	// KindEvent é algo que aconteceu num turno (ação, narração, efeito).
	KindEvent Kind = "event"
	// KindSummary é o resumo de um conjunto de entradas compactadas.
	KindSummary Kind = "summary"
)

// DefaultImportance é usada quando Entry.Importance é zero.
const DefaultImportance = 0.5

// Erros sentinela.
var (
	ErrInvalidEntry = errors.New("memory: entrada inválida")
	ErrInvalidQuery = errors.New("memory: consulta inválida")
)

// Entry é uma unidade de memória.
type Entry struct {
	ID            string    `json:"id"`
	CampaignID    string    `json:"campaign_id"`
	SessionNumber int       `json:"session"`
	At            time.Time `json:"at"`
	Kind          Kind      `json:"kind"`
	// Actor é quem originou a entrada (ID do personagem, "mestre"...).
	Actor string `json:"actor,omitempty"`
	Text  string `json:"text"`
	// Refs são as entidades da base de conhecimento citadas na entrada.
	Refs []knowledge.Ref `json:"refs,omitempty"`
	// Importance em [0,1]; 0 é tratado como DefaultImportance.
	Importance float64 `json:"importance,omitempty"`
	// Summarizes lista os IDs das entradas que este resumo substituiu
	// (só para KindSummary). As originais ficam no arquivo como arquivadas.
	Summarizes []string `json:"summarizes,omitempty"`
	// Embedding é preenchido no Record quando a Store tem um Embedder.
	Embedding []float32 `json:"embedding,omitempty"`
}

// Query descreve uma busca na memória de uma campanha.
type Query struct {
	CampaignID string
	// Text é a consulta em texto livre (ação do jogador, cena atual...).
	Text string
	// Refs são entidades em cena; entradas que as citam ganham bônus.
	Refs []knowledge.Ref
	// FilterRefs, se não vazio, restringe às entradas que citam ao menos
	// uma dessas Refs (filtro duro, diferente do bônus de Refs).
	FilterRefs []knowledge.Ref
	// MinSession/MaxSession restringem por número de sessão (0 = sem limite).
	MinSession, MaxSession int
	// Kinds restringe por tipo de entrada (vazio = todos).
	Kinds []Kind
	// CurrentSession é a referência de recência; 0 = maior sessão na memória.
	CurrentSession int
	// Limit é o top-k; 0 = DefaultLimit.
	Limit int
}

// DefaultLimit é o top-k padrão do Recall.
const DefaultLimit = 10

// Breakdown expõe cada componente do score (todos em [0,1]) para que o
// ranking seja explicável em logs e testes.
type Breakdown struct {
	Lexical    float64 `json:"lexical"`
	Vector     float64 `json:"vector"`
	Ref        float64 `json:"ref"`
	Recency    float64 `json:"recency"`
	Importance float64 `json:"importance"`
}

// Scored é uma entrada com sua pontuação.
type Scored struct {
	Entry     Entry
	Score     float64
	Breakdown Breakdown
}

// Memory é o contrato consumido pelo loop de jogo.
type Memory interface {
	// Record grava a entrada e a devolve com ID/At/Kind preenchidos.
	Record(ctx context.Context, e Entry) (Entry, error)
	// Recall devolve as entradas mais relevantes, em ordem de score.
	Recall(ctx context.Context, q Query) ([]Scored, error)
	// List devolve as entradas ativas (não arquivadas) em ordem cronológica.
	List(ctx context.Context, campaignID string) ([]Entry, error)
}

// Embedder transforma textos em vetores (modelo local ou remoto). É
// opcional: sem ele o ranking é puramente lexical. Nenhuma implementação
// real existe ainda; o ponto de troca é este.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

func (e Entry) importance() float64 {
	if e.Importance <= 0 {
		return DefaultImportance
	}
	return min(e.Importance, 1)
}

func hasRef(refs []knowledge.Ref, r knowledge.Ref) bool {
	for _, x := range refs {
		if x == r {
			return true
		}
	}
	return false
}
