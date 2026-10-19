package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// Summarizer condensa um conjunto de entradas (em ordem cronológica) num
// único texto de resumo.
type Summarizer interface {
	Summarize(ctx context.Context, entries []Entry) (string, error)
}

// ErrEmptySummary indica que o summarizer não produziu texto.
var ErrEmptySummary = errors.New("memory: resumo vazio")

// ExtractiveSummarizer é o fallback determinístico, sem LLM: escolhe as
// entradas mais importantes (empate: mais recentes), mantém a ordem
// cronológica e concatena até MaxRunes. Fatos vêm antes de eventos em caso
// de empate de importância, porque costumam ser o que importa lembrar.
type ExtractiveSummarizer struct {
	// MaxRunes limita o tamanho do resumo; 0 = 600.
	MaxRunes int
}

// Summarize implementa Summarizer.
func (s ExtractiveSummarizer) Summarize(ctx context.Context, entries []Entry) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	limit := s.MaxRunes
	if limit <= 0 {
		limit = 600
	}
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := entries[idx[a]], entries[idx[b]]
		if ea.importance() != eb.importance() {
			return ea.importance() > eb.importance()
		}
		if (ea.Kind == KindFact) != (eb.Kind == KindFact) {
			return ea.Kind == KindFact
		}
		return idx[a] > idx[b]
	})

	chosen := map[int]bool{}
	used := 0
	for _, i := range idx {
		n := utf8.RuneCountInString(line(entries[i])) + 1
		if used+n > limit {
			continue
		}
		chosen[i] = true
		used += n
	}
	var parts []string
	for i, e := range entries {
		if chosen[i] {
			parts = append(parts, line(e))
		}
	}
	if len(parts) == 0 {
		return "", ErrEmptySummary
	}
	return strings.Join(parts, " "), nil
}

func line(e Entry) string {
	t := strings.TrimSpace(e.Text)
	if !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "!") && !strings.HasSuffix(t, "?") {
		t += "."
	}
	return t
}

// LLMSummarizer resume via llm.Provider. Se o provider responder no
// contrato [NARRATIVE]/[MECHANICS], só a narrativa é usada; a mecânica é
// ignorada (um resumo nunca altera estado).
type LLMSummarizer struct {
	Provider llm.Provider
	// MaxTokens da resposta; 0 = 300.
	MaxTokens int
}

const summarizeSystem = `Você é o cronista de uma campanha de RPG de mesa.
Resuma os registros abaixo em um parágrafo curto, em português, na terceira pessoa.
Preserve nomes de NPCs, lugares e itens, promessas, dívidas, pistas e itens perdidos ou obtidos.
Não invente nada que não esteja nos registros. Não inclua mecânica de jogo.`

// Summarize implementa Summarizer.
func (s LLMSummarizer) Summarize(ctx context.Context, entries []Entry) (string, error) {
	if s.Provider == nil {
		return "", llm.ErrNotConfigured
	}
	maxTok := s.MaxTokens
	if maxTok <= 0 {
		maxTok = 300
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "- [sessão %d · %s]", e.SessionNumber, e.Kind)
		if e.Actor != "" {
			fmt.Fprintf(&b, " %s:", e.Actor)
		}
		fmt.Fprintf(&b, " %s\n", e.Text)
	}
	resp, err := s.Provider.Generate(ctx, llm.Request{
		System:    summarizeSystem,
		Prompt:    b.String(),
		MaxTokens: maxTok,
		Tags:      map[string]string{"kind": "summary"},
	})
	if err != nil {
		return "", err
	}
	parsed, _ := llm.ParseResponse(resp.Text)
	out := strings.TrimSpace(parsed.Narrative)
	if out == "" {
		return "", ErrEmptySummary
	}
	return out, nil
}

// Compactor substitui entradas antigas por resumos.
//
// A compactação nunca apaga: as entradas resumidas vão para a lista de
// arquivadas do arquivo da campanha e o resumo registra seus IDs em
// Entry.Summarizes.
type Compactor struct {
	Store *Store
	// Summarizer principal; nil = ExtractiveSummarizer.
	Summarizer Summarizer
	// Fallback usado se o principal falhar; nil = ExtractiveSummarizer.
	Fallback Summarizer
	// MaxEntries é o limite de entradas ativas que dispara MaybeCompact;
	// 0 = 200.
	MaxEntries int
}

func (c *Compactor) summarize(ctx context.Context, es []Entry) (string, error) {
	fb := c.Fallback
	if fb == nil {
		fb = ExtractiveSummarizer{}
	}
	if c.Summarizer != nil {
		txt, err := c.Summarizer.Summarize(ctx, es)
		if err == nil && strings.TrimSpace(txt) != "" {
			return strings.TrimSpace(txt), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return fb.Summarize(ctx, es)
}

// CompactSession resume todas as entradas não-resumo da sessão indicada
// (chame ao encerrar a sessão). Devolve ok=false se não havia o que
// compactar.
func (c *Compactor) CompactSession(ctx context.Context, campaignID string, session int) (Entry, bool, error) {
	all, err := c.Store.List(ctx, campaignID)
	if err != nil {
		return Entry{}, false, err
	}
	var es []Entry
	for _, e := range all {
		if e.SessionNumber == session && e.Kind != KindSummary {
			es = append(es, e)
		}
	}
	if len(es) == 0 {
		return Entry{}, false, nil
	}

	// O summarizer (possivelmente um LLM) roda sem lock; o replace só
	// arquiva as entradas que ainda existirem — entradas gravadas nesse
	// meio-tempo continuam ativas.
	txt, err := c.summarize(ctx, es)
	if err != nil {
		return Entry{}, false, err
	}
	ids := make([]string, len(es))
	var refs []knowledge.Ref
	imp := 0.0
	for i, e := range es {
		ids[i] = e.ID
		imp = max(imp, e.importance())
		for _, r := range e.Refs {
			if !hasRef(refs, r) {
				refs = append(refs, r)
			}
		}
	}
	return c.Store.replace(ctx, campaignID, ids, Entry{
		SessionNumber: session,
		At:            es[len(es)-1].At,
		Actor:         "memoria",
		Text:          fmt.Sprintf("Resumo da sessão %d: %s", session, txt),
		Refs:          refs,
		Importance:    imp,
	})
}

// MaybeCompact compacta, da sessão mais antiga para a mais nova (sem
// tocar em currentSession), enquanto houver mais de MaxEntries entradas
// ativas. Devolve os resumos criados.
func (c *Compactor) MaybeCompact(ctx context.Context, campaignID string, currentSession int) ([]Entry, error) {
	limit := c.MaxEntries
	if limit <= 0 {
		limit = 200
	}
	var created []Entry
	for {
		all, err := c.Store.List(ctx, campaignID)
		if err != nil || len(all) <= limit {
			return created, err
		}
		oldest, found := 0, false
		for _, e := range all {
			if e.Kind != KindSummary && e.SessionNumber < currentSession {
				oldest, found = e.SessionNumber, true
				break // List é cronológica
			}
		}
		if !found {
			return created, nil // só resta a sessão atual (ou resumos)
		}
		sum, ok, err := c.CompactSession(ctx, campaignID, oldest)
		if err != nil {
			return created, err
		}
		if ok {
			created = append(created, sum)
		}
	}
}
