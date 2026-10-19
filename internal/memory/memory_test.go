package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/events"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/llm/local"
)

var (
	ctx      = context.Background()
	guardiao = knowledge.Ref{Type: knowledge.RefNPC, ID: "guardiao"}
	espada   = knowledge.Ref{Type: knowledge.RefItem, ID: "espada-curta"}
	cripta   = knowledge.Ref{Type: knowledge.RefEnvironment, ID: "entrada-cripta"}
)

// clock devolve um relógio que avança 1 minuto por chamada.
func clock() func() time.Time {
	var mu sync.Mutex
	t := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Minute)
		return t
	}
}

func rec(t *testing.T, m Memory, e Entry) Entry {
	t.Helper()
	if e.CampaignID == "" {
		e.CampaignID = "c1"
	}
	got, err := m.Record(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ids(ss []Scored) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Entry.ID
	}
	return out
}

func TestTokenizeNormalizesAccentsAndPlurals(t *testing.T) {
	got := strings.Join(tokenize("O velho Guardião e os GUARDIÕES da Cripta, além das promessas!"), " ")
	want := "velho guardiao guardiao cripta alem promessa"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRecallAccentInsensitive(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	e := rec(t, m, Entry{SessionNumber: 1, Kind: KindFact, Text: "O velho guardião se chama Odran."})
	rec(t, m, Entry{SessionNumber: 1, Text: "O grupo acendeu tochas."})

	for _, q := range []string{"guardiao", "GUARDIÃO", "guardiões"} {
		res, err := m.Recall(ctx, Query{CampaignID: "c1", Text: q})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Entry.ID != e.ID {
			t.Fatalf("%q: got %v", q, ids(res))
		}
	}
}

func TestRecallRelevance(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	rec(t, m, Entry{SessionNumber: 1, Text: "O grupo entrou na cripta escura."})
	promise := rec(t, m, Entry{SessionNumber: 1, Kind: KindFact, Text: "O grupo fez a promessa de devolver o amuleto de prata ao guardião."})
	rec(t, m, Entry{SessionNumber: 1, Text: "Aria comprou uma tocha na vila."})
	rec(t, m, Entry{SessionNumber: 1, Text: "O amuleto brilhou na escuridão."})

	res, err := m.Recall(ctx, Query{CampaignID: "c1", Text: "qual foi a promessa sobre o amuleto?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Entry.ID != promise.ID {
		t.Fatalf("got %v", ids(res))
	}
	if res[0].Breakdown.Lexical != 1 || res[1].Breakdown.Lexical >= 1 {
		t.Fatalf("breakdown %+v / %+v", res[0].Breakdown, res[1].Breakdown)
	}
	for _, s := range res {
		if s.Score <= 0 || s.Score > 1 {
			t.Fatalf("score fora de [0,1]: %v", s.Score)
		}
	}
}

func TestRecallRareTermWins(t *testing.T) {
	// IDF: "Odran" aparece uma vez, "grupo" em todas; quem cita Odran ganha.
	m := NewInMemory(WithClock(clock()))
	rec(t, m, Entry{SessionNumber: 1, Text: "O grupo descansou."})
	rec(t, m, Entry{SessionNumber: 1, Text: "O grupo lutou."})
	odran := rec(t, m, Entry{SessionNumber: 1, Text: "O grupo conheceu Odran."})
	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "grupo Odran"})
	if res[0].Entry.ID != odran.ID {
		t.Fatalf("got %v", ids(res))
	}
}

func TestRecallRefBonusAndFilter(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	a := rec(t, m, Entry{SessionNumber: 1, Text: "Uma figura encapuzada observava.", Refs: []knowledge.Ref{guardiao}})
	b := rec(t, m, Entry{SessionNumber: 1, Text: "A espada caiu no poço.", Refs: []knowledge.Ref{espada, cripta}})
	rec(t, m, Entry{SessionNumber: 1, Text: "Choveu a noite toda."})

	// Bônus: sem nenhuma palavra em comum, a Ref em cena traz a entrada.
	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "quem está aí?", Refs: []knowledge.Ref{guardiao}})
	if len(res) != 1 || res[0].Entry.ID != a.ID || res[0].Breakdown.Ref != 1 {
		t.Fatalf("bônus: %v", ids(res))
	}

	// Filtro duro: só entradas que citam a espada, mesmo com texto vazio.
	res, _ = m.Recall(ctx, Query{CampaignID: "c1", FilterRefs: []knowledge.Ref{espada}})
	if len(res) != 1 || res[0].Entry.ID != b.ID {
		t.Fatalf("filtro: %v", ids(res))
	}

	// Ref parcial: 1 de 2 Refs da consulta.
	res, _ = m.Recall(ctx, Query{CampaignID: "c1", Refs: []knowledge.Ref{espada, guardiao}})
	if len(res) != 2 || res[0].Breakdown.Ref != 0.5 {
		t.Fatalf("parcial: %+v", res)
	}
}

func TestRecallRecencyAndImportance(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	rec(t, m, Entry{SessionNumber: 1, Text: "Encontraram uma chave de ferro."})
	newer := rec(t, m, Entry{SessionNumber: 4, Text: "Encontraram uma chave de ferro."})

	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "chave"})
	if ids(res)[0] != newer.ID {
		t.Fatalf("recência: %v", ids(res))
	}
	// 3 sessões de distância = meia-vida.
	if res[1].Breakdown.Recency != 0.5 || res[0].Breakdown.Recency != 1 {
		t.Fatalf("recency %+v %+v", res[0].Breakdown, res[1].Breakdown)
	}

	// Importância alta supera a diferença de recência.
	m2 := NewInMemory(WithClock(clock()))
	imp := rec(t, m2, Entry{SessionNumber: 3, Importance: 1, Text: "Encontraram uma chave de ferro."})
	rec(t, m2, Entry{SessionNumber: 4, Importance: 0.1, Text: "Encontraram uma chave de ferro."})
	res, _ = m2.Recall(ctx, Query{CampaignID: "c1", Text: "chave"})
	if ids(res)[0] != imp.ID {
		t.Fatalf("importância: %v %+v", ids(res), res)
	}
}

func TestRecallFiltersAndLimit(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	for s := 1; s <= 5; s++ {
		rec(t, m, Entry{SessionNumber: s, Text: fmt.Sprintf("Na sessão %d o dragão rugiu.", s)})
		rec(t, m, Entry{SessionNumber: s, Kind: KindFact, Text: "O dragão guarda ouro."})
	}
	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "dragão", MinSession: 2, MaxSession: 3, Kinds: []Kind{KindEvent}})
	if len(res) != 2 {
		t.Fatalf("got %d", len(res))
	}
	for _, s := range res {
		if s.Entry.SessionNumber < 2 || s.Entry.SessionNumber > 3 || s.Entry.Kind != KindEvent {
			t.Fatalf("filtro violado: %+v", s.Entry)
		}
	}
	res, _ = m.Recall(ctx, Query{CampaignID: "c1", Text: "dragão", Limit: 3})
	if len(res) != 3 {
		t.Fatalf("limit: %d", len(res))
	}
	// Sem texto/Refs: tudo entra, ordenado por recência/importância.
	res, _ = m.Recall(ctx, Query{CampaignID: "c1", Limit: 100})
	if len(res) != 10 || res[0].Entry.SessionNumber != 5 {
		t.Fatalf("sem sinal: %v", ids(res))
	}
	if _, err := m.Recall(ctx, Query{CampaignID: "c1", Limit: -1}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatal(err)
	}
}

func TestRecallDeterministic(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	for i := 0; i < 20; i++ {
		rec(t, m, Entry{SessionNumber: 1 + i%3, Text: "O guardião vigia a porta da cripta."})
	}
	first, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "guardião cripta"})
	for i := 0; i < 5; i++ {
		again, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "guardião cripta"})
		if strings.Join(ids(first), ",") != strings.Join(ids(again), ",") {
			t.Fatal("ranking não determinístico")
		}
	}
}

func TestRecordValidation(t *testing.T) {
	m := NewInMemory()
	cases := []Entry{
		{CampaignID: "c1", Text: "   "},
		{CampaignID: "../x", Text: "a"},
		{CampaignID: "c1", Text: "a", SessionNumber: -1},
		{CampaignID: "c1", Text: "a", Refs: []knowledge.Ref{{Type: "planeta", ID: "x"}}},
	}
	for _, e := range cases {
		if _, err := m.Record(ctx, e); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("%+v: %v", e, err)
		}
	}
	got := rec(t, m, Entry{Text: "ok", Importance: 7})
	if got.ID != "m-000001" || got.Kind != KindEvent || got.At.IsZero() || got.Importance != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	root := t.TempDir()
	m, err := Open(root, WithClock(clock()))
	if err != nil {
		t.Fatal(err)
	}
	a := rec(t, m, Entry{SessionNumber: 1, Kind: KindFact, Actor: "mestre", Text: "O guardião se chama Odran.", Refs: []knowledge.Ref{guardiao}, Importance: 0.9})
	rec(t, m, Entry{SessionNumber: 1, Text: "A espada foi perdida no poço.", Refs: []knowledge.Ref{espada}})
	rec(t, m, Entry{CampaignID: "c2", Text: "outra campanha"})

	if _, err := os.Stat(filepath.Join(root, "memory", "c1.json")); err != nil {
		t.Fatal(err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(root, "memory", ".tmp-*")); len(tmp) != 0 {
		t.Fatalf("temporários sobrando: %v", tmp)
	}

	m2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	list, err := m2.List(ctx, "c1")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	g := list[0]
	if g.ID != a.ID || g.Text != a.Text || g.Actor != "mestre" || g.Importance != 0.9 ||
		len(g.Refs) != 1 || g.Refs[0] != guardiao || !g.At.Equal(a.At) {
		t.Fatalf("round-trip: %+v vs %+v", g, a)
	}
	// O contador de IDs também persiste.
	c := rec(t, m2, Entry{Text: "nova"})
	if c.ID != "m-000003" {
		t.Fatalf("id %s", c.ID)
	}
	res, _ := m2.Recall(ctx, Query{CampaignID: "c1", Text: "Odran"})
	if len(res) != 1 || res[0].Entry.ID != a.ID {
		t.Fatalf("recall após reabrir: %v", ids(res))
	}
	other, _ := m2.List(ctx, "c2")
	if len(other) != 1 {
		t.Fatal("campanhas devem ser isoladas")
	}
}

func TestCorruptFile(t *testing.T) {
	root := t.TempDir()
	m, _ := Open(root)
	os.WriteFile(filepath.Join(root, "memory", "c1.json"), []byte("{nope"), 0o644)
	if _, err := m.List(ctx, "c1"); err == nil {
		t.Fatal("esperava erro de arquivo corrompido")
	}
}

func TestConcurrentRecordRecall(t *testing.T) {
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const writers, per = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if _, err := m.Record(ctx, Entry{CampaignID: "c1", SessionNumber: 1 + w%3, Text: fmt.Sprintf("guardião %d viu %d", w, i), Refs: []knowledge.Ref{guardiao}}); err != nil {
					t.Error(err)
				}
			}
		}(w)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if _, err := m.Recall(ctx, Query{CampaignID: "c1", Text: "guardião", Refs: []knowledge.Ref{guardiao}}); err != nil {
					t.Error(err)
				}
				m.List(ctx, "c1")
			}
		}()
	}
	wg.Wait()
	list, _ := m.List(ctx, "c1")
	if len(list) != writers*per {
		t.Fatalf("got %d", len(list))
	}
	seen := map[string]bool{}
	for _, e := range list {
		if seen[e.ID] {
			t.Fatalf("ID duplicado %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestListReturnsCopies(t *testing.T) {
	m := NewInMemory()
	rec(t, m, Entry{Text: "a", Refs: []knowledge.Ref{guardiao}})
	l, _ := m.List(ctx, "c1")
	l[0].Refs[0] = espada
	l2, _ := m.List(ctx, "c1")
	if l2[0].Refs[0] != guardiao {
		t.Fatal("List vazou estado interno")
	}
}

// --- ContextBuilder ---

func TestContextBuilderBudget(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	rec(t, m, Entry{SessionNumber: 2, Importance: 1, Text: "O guardião " + strings.Repeat("muito ", 60) + "antigo."})
	short1 := rec(t, m, Entry{SessionNumber: 1, Text: "O guardião se chama Odran."})
	short2 := rec(t, m, Entry{SessionNumber: 2, Text: "O guardião pediu o amuleto."})
	b := ContextBuilder{Memory: m}

	q := Query{CampaignID: "c1", Text: "guardião"}
	full, err := b.Build(ctx, q, 1000)
	if err != nil || len(full.Entries) != 3 {
		t.Fatalf("%+v %v", full, err)
	}

	// Orçamento que não comporta a entrada longa: ela é pulada inteira e
	// as menores (de score mais baixo) ainda entram.
	budget := EstimateTokens(DefaultHeader+"\n") + EstimateTokens(Render(short1)+"\n") + EstimateTokens(Render(short2)+"\n")
	blk, _ := b.Build(ctx, q, budget)
	if blk.Tokens > budget {
		t.Fatalf("estourou: %d > %d", blk.Tokens, budget)
	}
	if strings.Contains(blk.Text, "muito") || len(blk.Entries) != 2 {
		t.Fatalf("entrada longa deveria ser pulada:\n%s", blk.Text)
	}
	// Ordem cronológica no texto: sessão 1 antes da 2.
	if strings.Index(blk.Text, "Odran") > strings.Index(blk.Text, "amuleto") {
		t.Fatalf("ordem:\n%s", blk.Text)
	}
	for _, l := range strings.Split(strings.TrimSpace(blk.Text), "\n")[1:] {
		if !strings.HasPrefix(l, "- [sessão ") || !strings.HasSuffix(l, ".") {
			t.Fatalf("linha cortada ou malformada: %q", l)
		}
	}
	if !strings.HasPrefix(blk.Text, DefaultHeader+"\n") {
		t.Fatal(blk.Text)
	}

	// Orçamento menor que o cabeçalho + qualquer entrada: bloco vazio.
	if blk, _ := b.Build(ctx, q, 5); blk.Text != "" || len(blk.Entries) != 0 {
		t.Fatalf("%+v", blk)
	}
	if blk, _ := b.Build(ctx, q, 0); blk.Text != "" {
		t.Fatal("orçamento 0")
	}
}

func TestRender(t *testing.T) {
	got := Render(Entry{SessionNumber: 2, Kind: KindEvent, Actor: "aria", Text: "abre\na porta", Refs: []knowledge.Ref{cripta, espada}})
	want := "- [sessão 2 · evento] aria: abre a porta (environment:entrada-cripta, item:espada-curta)"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

// --- Embedder ---

// fakeEmbedder mapeia palavras-chave para eixos: "sinônimos" sem
// sobreposição lexical ficam próximos.
type fakeEmbedder struct{ fail bool }

func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("offline")
	}
	axes := [][]string{{"fogo", "chama", "incendio"}, {"agua", "rio", "chuva"}}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, len(axes))
		for _, tok := range tokenize(t) {
			for a, words := range axes {
				for _, w := range words {
					if tok == w {
						v[a]++
					}
				}
			}
		}
		out[i] = v
	}
	return out, nil
}

func TestEmbedderCombinesScores(t *testing.T) {
	m := NewInMemory(WithClock(clock()), WithEmbedder(fakeEmbedder{}))
	fire := rec(t, m, Entry{SessionNumber: 1, Text: "Um incêndio tomou a taverna."})
	rec(t, m, Entry{SessionNumber: 1, Text: "O rio transbordou com a chuva."})
	if len(fire.Embedding) != 2 {
		t.Fatal("embedding não calculado no Record")
	}
	// "chamas" não aparece em nenhuma entrada; só o vetor encontra.
	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "chamas"})
	if len(res) != 1 || res[0].Entry.ID != fire.ID || res[0].Breakdown.Vector != 1 || res[0].Breakdown.Lexical != 0 {
		t.Fatalf("%+v", res)
	}

	// Embedder falhando: degrada para lexical sem erro.
	m2 := NewInMemory(WithEmbedder(fakeEmbedder{fail: true}))
	rec(t, m2, Entry{Text: "Um incêndio tomou a taverna."})
	res, err := m2.Recall(ctx, Query{CampaignID: "c1", Text: "incêndio"})
	if err != nil || len(res) != 1 || res[0].Breakdown.Vector != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// --- Compactação ---

type fakeSummarizer struct {
	text  string
	err   error
	calls int
	got   []Entry
}

func (f *fakeSummarizer) Summarize(_ context.Context, es []Entry) (string, error) {
	f.calls++
	f.got = es
	return f.text, f.err
}

func seedSessions(t *testing.T, m Memory) {
	rec(t, m, Entry{SessionNumber: 1, Kind: KindFact, Importance: 0.9, Text: "O guardião Odran guarda a cripta.", Refs: []knowledge.Ref{guardiao}})
	rec(t, m, Entry{SessionNumber: 1, Text: "Aria perdeu a espada curta no poço.", Refs: []knowledge.Ref{espada}})
	rec(t, m, Entry{SessionNumber: 1, Importance: 0.1, Text: "O grupo jantou ensopado."})
	rec(t, m, Entry{SessionNumber: 2, Text: "O grupo voltou à cripta."})
}

func TestCompactSessionWithSummarizer(t *testing.T) {
	root := t.TempDir()
	m, _ := Open(root, WithClock(clock()))
	seedSessions(t, m)
	fs := &fakeSummarizer{text: "Odran guarda a cripta; a espada sumiu no poço."}
	c := &Compactor{Store: m, Summarizer: fs}

	sum, ok, err := c.CompactSession(ctx, "c1", 1)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(fs.got) != 3 {
		t.Fatalf("summarizer recebeu %d", len(fs.got))
	}
	if sum.Kind != KindSummary || sum.SessionNumber != 1 || !strings.Contains(sum.Text, "espada sumiu") ||
		strings.Join(sum.Summarizes, ",") != "m-000001,m-000002,m-000003" ||
		len(sum.Refs) != 2 || sum.Importance != 0.9 {
		t.Fatalf("%+v", sum)
	}

	list, _ := m.List(ctx, "c1")
	if len(list) != 2 || list[0].ID != sum.ID {
		t.Fatalf("ativas: %+v", list)
	}
	arch, _ := m.Archived(ctx, "c1")
	if len(arch) != 3 {
		t.Fatalf("arquivadas: %d", len(arch))
	}

	// O resumo é recuperável e as originais não aparecem mais no Recall.
	res, _ := m.Recall(ctx, Query{CampaignID: "c1", Text: "espada", Refs: []knowledge.Ref{espada}})
	if len(res) != 1 || res[0].Entry.ID != sum.ID {
		t.Fatalf("%v", ids(res))
	}

	// Persistido: reabrir mantém resumo e arquivo.
	m2, _ := Open(root)
	arch2, _ := m2.Archived(ctx, "c1")
	list2, _ := m2.List(ctx, "c1")
	if len(arch2) != 3 || len(list2) != 2 {
		t.Fatal("compactação não persistiu")
	}

	// Recompactar a mesma sessão não resume o resumo.
	if _, ok, _ := c.CompactSession(ctx, "c1", 1); ok {
		t.Fatal("nada deveria ser compactado")
	}
}

func TestCompactFallback(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	seedSessions(t, m)
	c := &Compactor{Store: m, Summarizer: &fakeSummarizer{err: errors.New("llm fora do ar")}}
	sum, ok, err := c.CompactSession(ctx, "c1", 1)
	if err != nil || !ok {
		t.Fatal(err)
	}
	// Extrativo: frases originais, em ordem cronológica.
	want := "Resumo da sessão 1: O guardião Odran guarda a cripta. Aria perdeu a espada curta no poço. O grupo jantou ensopado."
	if sum.Text != want {
		t.Fatalf("got %q", sum.Text)
	}
}

func TestExtractiveSummarizerRespectsLimit(t *testing.T) {
	es := []Entry{
		{Text: "O grupo jantou ensopado", Importance: 0.1},
		{Text: "Odran é o guardião", Kind: KindFact, Importance: 0.9},
		{Text: "A espada foi perdida", Importance: 0.8},
	}
	got, err := ExtractiveSummarizer{MaxRunes: 45}.Summarize(ctx, es)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Odran é o guardião. A espada foi perdida." {
		t.Fatalf("got %q", got)
	}
	if _, err := (ExtractiveSummarizer{MaxRunes: 3}).Summarize(ctx, es); !errors.Is(err, ErrEmptySummary) {
		t.Fatal(err)
	}
}

func TestLLMSummarizer(t *testing.T) {
	s := LLMSummarizer{Provider: local.NewEchoProvider()}
	got, err := s.Summarize(ctx, []Entry{{SessionNumber: 1, Kind: KindFact, Actor: "mestre", Text: "Odran guarda a cripta"}})
	if err != nil {
		t.Fatal(err)
	}
	// O Echo devolve o prompt dentro de [NARRATIVE]; a mecânica é descartada.
	if !strings.Contains(got, "Odran guarda a cripta") || strings.Contains(got, "[MECHANICS]") {
		t.Fatalf("got %q", got)
	}
	if _, err := (LLMSummarizer{}).Summarize(ctx, nil); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatal(err)
	}
}

func TestMaybeCompact(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	for s := 1; s <= 4; s++ {
		for i := 0; i < 5; i++ {
			rec(t, m, Entry{SessionNumber: s, Text: fmt.Sprintf("evento %d da sessão %d", i, s)})
		}
	}
	c := &Compactor{Store: m, MaxEntries: 12}
	created, err := c.MaybeCompact(ctx, "c1", 4)
	if err != nil {
		t.Fatal(err)
	}
	// 20 → (s1) 16 → (s2) 12: duas sessões mais antigas compactadas.
	if len(created) != 2 || created[0].SessionNumber != 1 || created[1].SessionNumber != 2 {
		t.Fatalf("%+v", created)
	}
	list, _ := m.List(ctx, "c1")
	if len(list) != 12 {
		t.Fatalf("ativas: %d", len(list))
	}

	// A sessão atual nunca é compactada, mesmo acima do limite.
	c.MaxEntries = 1
	c.MaybeCompact(ctx, "c1", 4)
	list, _ = m.List(ctx, "c1")
	cur := 0
	for _, e := range list {
		if e.SessionNumber == 4 {
			cur++
		}
	}
	if cur != 5 {
		t.Fatalf("sessão atual mexida: %d", cur)
	}
}

// --- Eventos ---

func TestEventRecorder(t *testing.T) {
	m := NewInMemory(WithClock(clock()))
	d := events.NewDispatcher()
	r := &EventRecorder{
		Memory: m,
		Locate: func() (string, int) { return "c1", 3 },
		Resolve: func(text string) []knowledge.Ref {
			if strings.Contains(normalize(text), "guardiao") {
				return []knowledge.Ref{guardiao}
			}
			return nil
		},
		Compactor: &Compactor{Store: m},
	}
	d.SubscribeAll(r.Handle)

	at := time.Date(2026, 2, 1, 21, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.Publish(events.Event{Type: events.PlayerAction, At: at, Payload: campaign.LogEntry{Actor: "aria", Kind: "action", Text: "Falo com o guardião."}}))
	must(d.Publish(events.Event{Type: events.EffectApplied, Payload: &campaign.LogEntry{Actor: "mestre", Kind: "effect", Text: "Aria perde a espada."}}))
	must(d.Publish(events.Event{Type: events.NarrationDone, Payload: Entry{Kind: KindFact, Text: "O guardião se chama Odran."}}))
	must(d.Publish(events.Event{Type: events.TurnPhaseChanged, Payload: "ignorado"}))

	list, _ := m.List(ctx, "c1")
	if len(list) != 3 {
		t.Fatalf("%+v", list)
	}
	if e := list[0]; e.SessionNumber != 3 || e.Actor != "aria" || !e.At.Equal(at) || len(e.Refs) != 1 || e.Kind != KindEvent {
		t.Fatalf("%+v", e)
	}
	if list[1].Importance != 0.6 || list[2].Kind != KindFact {
		t.Fatalf("%+v", list)
	}

	must(d.Publish(events.Event{Type: events.SessionEnded}))
	list, _ = m.List(ctx, "c1")
	if len(list) != 1 || list[0].Kind != KindSummary || len(list[0].Summarizes) != 3 {
		t.Fatalf("compactação no fim da sessão: %+v", list)
	}
}

func TestFileFormat(t *testing.T) {
	root := t.TempDir()
	m, _ := Open(root)
	rec(t, m, Entry{Text: "x", Refs: []knowledge.Ref{guardiao}})
	data, _ := os.ReadFile(filepath.Join(root, "memory", "c1.json"))
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f["version"] != float64(1) || f["next_seq"] != float64(2) || f["campaign_id"] != "c1" {
		t.Fatalf("%s", data)
	}
}
