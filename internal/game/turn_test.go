package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/encounter"
	"github.com/rafvazl/mestre-virtual/internal/events"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/memory"
)

const okResponse = "[NARRATIVE]\nok\n[MECHANICS]\n"

// play executa um turno e confere de quem era a vez e a rodada.
func play(t *testing.T, eng *Engine, wantActor string, wantRound int) TurnResult {
	t.Helper()
	res, err := eng.PlayTurn(context.Background(), "ajo")
	if err != nil {
		t.Fatal(err)
	}
	if res.Actor != wantActor || res.Round != wantRound {
		t.Fatalf("turno de %s na rodada %d; esperava %s na %d", res.Actor, res.Round, wantActor, wantRound)
	}
	if eng.Phase() != encounter.AguardandoInput {
		t.Fatalf("fase %s ao fim do turno", eng.Phase())
	}
	return res
}

func TestInitiativeOrderAndRounds(t *testing.T) {
	f := newFixture(t, okResponse)
	play(t, f.eng, "aria", 1)
	play(t, f.eng, "bruno", 1)
	play(t, f.eng, "aria", 2)
	if f.eng.Current() != "bruno" || f.eng.Round() != 2 {
		t.Fatalf("vez de %s na rodada %d", f.eng.Current(), f.eng.Round())
	}

	// /passar: bruno passa sem chamar o LLM; a vez volta para aria (rodada 3).
	calls := f.prov.calls
	who, err := f.eng.Pass()
	if err != nil || who != "bruno" {
		t.Fatalf("pass: %s, %v", who, err)
	}
	if f.prov.calls != calls || f.eng.Current() != "aria" || f.eng.Round() != 3 {
		t.Fatalf("depois de passar: vez de %s na rodada %d (%d chamadas)", f.eng.Current(), f.eng.Round(), f.prov.calls)
	}
	last := f.eng.Session().Log[len(f.eng.Session().Log)-1]
	if last.Kind != KindPass || last.Actor != "bruno" {
		t.Fatalf("passagem no log: %+v", last)
	}
}

func TestDownCharacterIsSkipped(t *testing.T) {
	// Bruno tem PV 7 e o limite do teste é 10: o primeiro turno o derruba.
	f := newFixture(t, "[NARRATIVE]\nUma pedra cai.\n[MECHANICS]\nHP_CHANGE target=bruno amount=-10\n")
	play(t, f.eng, "aria", 1)
	bruno, _ := f.eng.Character("bruno")
	if bruno.HP != 0 {
		t.Fatalf("bruno PV %d", bruno.HP)
	}
	f.prov.text = okResponse
	play(t, f.eng, "aria", 2) // bruno pulado, mas continua no grupo
	if len(f.eng.Party()) != 2 {
		t.Fatal("personagem com PV 0 não deveria sair do grupo")
	}

	// Curado, volta à iniciativa.
	f.prov.text = "[NARRATIVE]\nCura.\n[MECHANICS]\nHP_CHANGE target=bruno amount=5\n"
	play(t, f.eng, "aria", 3)
	f.prov.text = okResponse
	play(t, f.eng, "bruno", 3)

	// Ao reabrir com bruno no chão e a vez salva com ele, a vez passa adiante.
	bruno.HP = 0
	if err := f.store.SaveCharacter(bruno); err != nil {
		t.Fatal(err)
	}
	f.eng.Session().Turn = &campaign.TurnState{Actor: "bruno", Round: 4}
	if err := f.store.SaveCampaign(f.eng.Campaign()); err != nil {
		t.Fatal(err)
	}
	again := f.open(t, OpenOptions{CampaignID: "cripta"})
	if again.Current() != "aria" || again.Round() != 5 {
		t.Fatalf("vez de %s na rodada %d", again.Current(), again.Round())
	}
}

func TestTurnPositionSurvivesReopen(t *testing.T) {
	f := newFixture(t, okResponse)
	play(t, f.eng, "aria", 1)

	again := f.open(t, OpenOptions{CampaignID: "cripta"})
	if again.Current() != "bruno" || again.Round() != 1 {
		t.Fatalf("retomada: vez de %s na rodada %d", again.Current(), again.Round())
	}
	play(t, again, "bruno", 1)
	play(t, again, "aria", 2)
	again = f.open(t, OpenOptions{CampaignID: "cripta"})
	play(t, again, "bruno", 2)

	// Sessão nova recomeça a iniciativa.
	if err := again.End(); err != nil {
		t.Fatal(err)
	}
	next := f.open(t, OpenOptions{CampaignID: "cripta"})
	if next.Session().Number != 2 || next.Current() != "aria" || next.Round() != 1 {
		t.Fatalf("sessão %d: vez de %s na rodada %d", next.Session().Number, next.Current(), next.Round())
	}

	// JSON anterior à Fase 4 (sem "turn") e ator salvo que saiu da campanha
	// recomeçam do primeiro da iniciativa.
	for name, ts := range map[string]*campaign.TurnState{"sem turn": nil, "ator desconhecido": {Actor: "zed", Round: 9}} {
		next.Session().Turn = ts
		if err := f.store.SaveCampaign(next.Campaign()); err != nil {
			t.Fatal(err)
		}
		if e := f.open(t, OpenOptions{CampaignID: "cripta"}); e.Current() != "aria" || e.Round() != 1 {
			t.Errorf("%s: vez de %s na rodada %d", name, e.Current(), e.Round())
		}
	}
}

// eventLog inscreve-se em todos os eventos e os resume em strings.
type eventLog struct{ got []string }

func (l *eventLog) handle(ev events.Event) error {
	s := string(ev.Type)
	switch p := ev.Payload.(type) {
	case PhaseChange:
		s += fmt.Sprintf(" %s>%s %s r%d", p.From, p.To, p.Actor, p.Round)
	case campaign.LogEntry:
		s += fmt.Sprintf(" [%s] %s", p.Kind, p.Actor)
	case SessionInfo:
		s += fmt.Sprintf(" %s#%d resumed=%v", p.CampaignID, p.Number, p.Resumed)
	default:
		s += fmt.Sprintf(" payload %T", p)
	}
	l.got = append(l.got, s)
	return nil
}

func TestEventsPublishedInOrder(t *testing.T) {
	var log eventLog
	f := newFixtureWith(t, "[NARRATIVE]\nOuro!\n[MECHANICS]\nITEM_GAINED target=aria item=moeda-ouro\nITEM_GAINED target=aria item=excalibur\n",
		func(f *fixture) {
			f.bus = events.NewDispatcher()
			f.bus.SubscribeAll(log.handle)
		})
	if _, err := f.eng.PlayTurn(context.Background(), "procuro"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Pass(); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.End(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"session.started cripta#1 resumed=false",
		"turn.phase_changed AguardandoInput>Resolvendo aria r1",
		"turn.phase_changed Resolvendo>Narrando aria r1",
		"player.action [action] aria",
		"narration.done [narration] mestre",
		"turn.phase_changed Narrando>AplicandoEfeitos aria r1",
		"effect.applied [effect] aria",
		"effect.rejected [effect_rejected] mestre",
		"turn.phase_changed AplicandoEfeitos>ProximoTurno aria r1",
		"turn.phase_changed ProximoTurno>AguardandoInput bruno r1",
		"player.action [pass] bruno",
		"turn.phase_changed AguardandoInput>ProximoTurno bruno r1",
		"turn.phase_changed ProximoTurno>AguardandoInput aria r2",
		"session.ended cripta#1 resumed=false",
	}
	if strings.Join(log.got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("eventos:\n%s\n\nesperado:\n%s", strings.Join(log.got, "\n"), strings.Join(want, "\n"))
	}
}

func TestHandlerFailureDoesNotBreakTurn(t *testing.T) {
	f := newFixtureWith(t, "[NARRATIVE]\nOuro!\n[MECHANICS]\nITEM_GAINED target=aria item=moeda-ouro\n", func(f *fixture) {
		f.bus = events.NewDispatcher()
		f.bus.Subscribe(events.PlayerAction, func(events.Event) error { panic("handler quebrado") })
		f.bus.Subscribe(events.EffectApplied, func(events.Event) error { return errors.New("disco cheio") })
	})
	res, err := f.eng.PlayTurn(context.Background(), "procuro")
	if err != nil {
		t.Fatal(err)
	}
	aria, _ := f.eng.Character("aria")
	if len(res.Applied) != 1 || aria.Inventory["moeda-ouro"] != 1 || f.eng.Current() != "bruno" {
		t.Fatalf("turno incompleto: %+v, vez de %s", res, f.eng.Current())
	}
	logs := f.logs.String()
	for _, want := range []string{"falha em handler de evento", "handler quebrado", "disco cheio", "event=player.action"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log sem %q:\n%s", want, logs)
		}
	}
}

func TestCancelMidStreamAppliesNothing(t *testing.T) {
	var log eventLog
	f := newFixtureWith(t, mixedResponse, func(f *fixture) {
		f.bus = events.NewDispatcher()
		f.bus.SubscribeAll(log.handle)
	})
	log.got = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.prov.afterFirst = cancel
	savesBefore := f.store.saves

	_, err := f.eng.PlayTurn(ctx, "ataco")
	if !errors.Is(err, ErrGeneration) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	aria, _ := f.eng.Character("aria")
	if aria.HP != aria.MaxHP || len(f.eng.Session().Log) != 0 || f.store.saves != savesBefore {
		t.Fatalf("estado alterado: aria %+v, log %d, saves %d→%d", aria, len(f.eng.Session().Log), savesBefore, f.store.saves)
	}
	if f.eng.Current() != "aria" || f.eng.Phase() != encounter.AguardandoInput {
		t.Fatalf("turno abortado deveria manter a vez: %s em %s", f.eng.Current(), f.eng.Phase())
	}
	if strings.Contains(f.out.String(), "goblin") {
		t.Errorf("narrativa depois do cancelamento: %q", f.out.String())
	}
	want := []string{
		"turn.phase_changed AguardandoInput>Resolvendo aria r1",
		"turn.phase_changed Resolvendo>Narrando aria r1",
		"turn.phase_changed Narrando>AguardandoInput aria r1",
	}
	if strings.Join(log.got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("eventos:\n%s", strings.Join(log.got, "\n"))
	}

	// O próximo turno, com contexto vivo, segue normalmente com aria.
	f.prov.afterFirst = nil
	f.prov.text = okResponse
	play(t, f.eng, "aria", 1)
}

var guardiao = knowledge.Ref{Type: knowledge.RefNPC, ID: "velho-guardiao"}

func TestMemoryInjectedIntoPrompt(t *testing.T) {
	mem := memory.NewInMemory()
	ctx := context.Background()
	for _, e := range []memory.Entry{
		{CampaignID: "cripta", SessionNumber: 1, Kind: memory.KindFact, Text: "O velho guardião se chama Odran.", Refs: []knowledge.Ref{guardiao}},
		{CampaignID: "cripta", SessionNumber: 1, Kind: memory.KindEvent, Text: "Bruno tropeçou nos degraus."},
	} {
		if _, err := mem.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	f := newFixtureWith(t, okResponse, func(f *fixture) { f.mem = mem })
	if _, err := f.eng.PlayTurn(ctx, "pergunto o nome ao velho guardião"); err != nil {
		t.Fatal(err)
	}
	prompt := f.prov.last.Prompt
	iMem, iAct := strings.Index(prompt, "## Memória da aventura"), strings.Index(prompt, "## Ação do jogador")
	if iMem < 0 || iMem > iAct {
		t.Fatalf("seção de memória ausente ou fora de ordem:\n%s", prompt)
	}
	if !strings.Contains(prompt[iMem:iAct], "Odran. (npc:velho-guardiao)") || !strings.Contains(prompt[iMem:iAct], memoryHeader) {
		t.Fatalf("memória sem o fato do guardião:\n%s", prompt[iMem:iAct])
	}
	if !strings.HasSuffix(prompt, "Aria (aria): pergunto o nome ao velho guardião") {
		t.Fatalf("a ação deve continuar na última linha:\n%s", prompt)
	}
}

func TestMemoryBudgetAndFailure(t *testing.T) {
	mem := memory.NewInMemory()
	if _, err := mem.Record(context.Background(), memory.Entry{CampaignID: "cripta", SessionNumber: 1,
		Text: strings.Repeat("guardião ", 200)}); err != nil {
		t.Fatal(err)
	}
	f := newFixtureWith(t, okResponse, func(f *fixture) { f.mem = mem })
	// Orçamento padrão (400 tokens) não comporta a entrada de ~450 tokens.
	play(t, f.eng, "aria", 1)
	if strings.Contains(f.prov.last.Prompt, "Memória da aventura") {
		t.Fatal("entrada acima do orçamento não deveria entrar no prompt")
	}

	// Memória quebrada não impede o turno.
	g := newFixtureWith(t, okResponse, func(f *fixture) { f.mem = failingMemory{} })
	play(t, g.eng, "aria", 1)
	if !strings.Contains(g.logs.String(), "memória indisponível") {
		t.Fatalf("log:\n%s", g.logs.String())
	}
}

type failingMemory struct{}

func (failingMemory) Record(context.Context, memory.Entry) (memory.Entry, error) {
	return memory.Entry{}, errors.New("memória fora do ar")
}
func (failingMemory) Recall(context.Context, memory.Query) ([]memory.Scored, error) {
	return nil, errors.New("memória fora do ar")
}
func (failingMemory) List(context.Context, string) ([]memory.Entry, error) { return nil, nil }

func TestEventRecorderWritesMemory(t *testing.T) {
	mem := memory.NewInMemory()
	f := newFixtureWith(t, "[NARRATIVE]\nO velho guardião entrega uma tocha.\n[MECHANICS]\nITEM_GAINED target=aria item=tocha\n",
		func(f *fixture) {
			f.mem = mem
			f.comp = &memory.Compactor{Store: mem}
		})
	ctx := context.Background()
	if _, err := f.eng.PlayTurn(ctx, "cumprimento o guardião"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Pass(); err != nil {
		t.Fatal(err)
	}
	list, err := mem.List(ctx, "cripta")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list {
		if e.SessionNumber != 1 || e.CampaignID != "cripta" {
			t.Errorf("entrada fora da sessão: %+v", e)
		}
		got = append(got, e.Actor+": "+e.Text)
	}
	want := []string{
		"aria: cumprimento o guardião",
		"mestre: O velho guardião entrega uma tocha.",
		"aria: Aria ganhou Tocha x1",
		"bruno: passa a vez",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("memória:\n%s", strings.Join(got, "\n"))
	}
	if refs := list[1].Refs; len(refs) != 2 || refs[0] != guardiao {
		t.Errorf("refs da narração: %v", refs)
	}

	// SessionEnded compacta a sessão num resumo.
	if err := f.eng.End(); err != nil {
		t.Fatal(err)
	}
	list, _ = mem.List(ctx, "cripta")
	if len(list) != 1 || list[0].Kind != memory.KindSummary || len(list[0].Summarizes) != 4 {
		t.Fatalf("depois do End: %+v", list)
	}
}

func TestRefIndex(t *testing.T) {
	f := newFixture(t, okResponse)
	got := f.eng.refs.Find("Pego a ESPADA CURTA perto do Velho guardião no salao-ossos")
	want := []knowledge.Ref{
		guardiao,
		{Type: knowledge.RefItem, ID: "espada-curta"},
		{Type: knowledge.RefEnvironment, ID: "salao-ossos"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("refs %v", got)
	}
	if got := f.eng.refs.Find("nada aqui"); len(got) != 0 {
		t.Fatalf("refs %v", got)
	}
}
