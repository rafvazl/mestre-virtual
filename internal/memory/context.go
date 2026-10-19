package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Recaller é o subconjunto de Memory de que o ContextBuilder precisa.
type Recaller interface {
	Recall(ctx context.Context, q Query) ([]Scored, error)
}

// DefaultHeader abre o bloco de memória no prompt.
const DefaultHeader = "[MEMÓRIA DA CAMPANHA]"

// ContextBuilder monta o bloco de memória injetado no prompt do LLM.
type ContextBuilder struct {
	Memory Recaller
	// Header é a primeira linha do bloco; "" = DefaultHeader.
	Header string
	// Candidates é quantas entradas pedir ao Recall antes de aplicar o
	// orçamento; 0 = 50.
	Candidates int
}

// Block é o resultado do ContextBuilder.
type Block struct {
	Text    string
	Entries []Scored // entradas incluídas, em ordem de score
	Tokens  int      // estimativa (EstimateTokens) de Text
}

// Build seleciona entradas por score até caber em budgetTokens e as
// renderiza, uma por linha, em ordem cronológica (mais natural para o LLM
// ler). Uma entrada nunca é cortada: se não cabe, é pulada e a próxima
// (menor) ainda pode entrar. Se nada couber, devolve Block vazio.
func (b ContextBuilder) Build(ctx context.Context, q Query, budgetTokens int) (Block, error) {
	if budgetTokens <= 0 {
		return Block{}, nil
	}
	if q.Limit == 0 {
		q.Limit = b.Candidates
		if q.Limit == 0 {
			q.Limit = 50
		}
	}
	scored, err := b.Memory.Recall(ctx, q)
	if err != nil {
		return Block{}, err
	}
	header := b.Header
	if header == "" {
		header = DefaultHeader
	}

	// Orçamento contado por linha (cada uma com seu "\n"); a soma das
	// estimativas por linha é sempre >= a estimativa do texto inteiro.
	used := EstimateTokens(header + "\n")
	var picked []Scored
	for _, s := range scored {
		cost := EstimateTokens(Render(s.Entry) + "\n")
		if used+cost > budgetTokens {
			continue
		}
		used += cost
		picked = append(picked, s)
	}
	if len(picked) == 0 {
		return Block{}, nil
	}

	chrono := make([]Entry, len(picked))
	for i, s := range picked {
		chrono[i] = s.Entry
	}
	sortChrono(chrono)
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteByte('\n')
	for _, e := range chrono {
		sb.WriteString(Render(e))
		sb.WriteByte('\n')
	}
	text := sb.String()
	return Block{Text: text, Entries: picked, Tokens: EstimateTokens(text)}, nil
}

var kindLabel = map[Kind]string{KindFact: "fato", KindEvent: "evento", KindSummary: "resumo"}

// Render formata uma entrada como uma linha do bloco de memória, ex.:
// "- [sessão 2 · fato] O velho guardião se chama Odran. (npc:guardiao)".
func Render(e Entry) string {
	label := kindLabel[e.Kind]
	if label == "" {
		label = string(e.Kind)
	}
	text := strings.Join(strings.Fields(e.Text), " ") // sem quebras de linha
	if e.Actor != "" && e.Kind == KindEvent {
		text = e.Actor + ": " + text
	}
	line := fmt.Sprintf("- [sessão %d · %s] %s", e.SessionNumber, label, text)
	if len(e.Refs) > 0 {
		refs := make([]string, len(e.Refs))
		for i, r := range e.Refs {
			refs[i] = r.String()
		}
		sort.Strings(refs)
		line += " (" + strings.Join(refs, ", ") + ")"
	}
	return line
}
