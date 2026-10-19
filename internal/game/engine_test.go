package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

// memStore é um storage em memória que guarda cópias serializadas, para que
// o teste veja apenas o que foi efetivamente persistido.
type memStore struct {
	camps, chars map[string][]byte
	saves        int
}

func newMemStore() *memStore {
	return &memStore{camps: map[string][]byte{}, chars: map[string][]byte{}}
}

func (m *memStore) SaveCampaign(c *campaign.Campaign) error {
	m.saves++
	b, err := json.Marshal(c)
	m.camps[c.ID] = b
	return err
}

func (m *memStore) LoadCampaign(id string) (*campaign.Campaign, error) {
	b, ok := m.camps[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	var c campaign.Campaign
	return &c, json.Unmarshal(b, &c)
}

func (m *memStore) ListCampaigns() ([]string, error) { return nil, nil }

func (m *memStore) SaveCharacter(c *character.Character) error {
	m.saves++
	b, err := json.Marshal(c)
	m.chars[c.ID] = b
	return err
}

func (m *memStore) LoadCharacter(id string) (*character.Character, error) {
	b, ok := m.chars[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	var c character.Character
	return &c, json.Unmarshal(b, &c)
}

func (m *memStore) ListCharacters() ([]string, error) { return nil, nil }

// fakeProvider devolve uma resposta fixa em pedaços pequenos, ou falha.
type fakeProvider struct {
	text     string
	startErr error // falha antes do stream
	midErr   error // falha depois do primeiro chunk
	last     llm.Request
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("não usado")
}

func (p *fakeProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.last = req
	if p.startErr != nil {
		return nil, p.startErr
	}
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		for i := 0; i < len(p.text); i += 4 {
			ch <- llm.Chunk{Text: p.text[i:min(i+4, len(p.text))]}
			if p.midErr != nil {
				ch <- llm.Chunk{Err: p.midErr}
				return
			}
		}
	}()
	return ch, nil
}

var testNow = time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)

type fixture struct {
	ks    knowledge.Store
	store *memStore
	prov  *fakeProvider
	out   *strings.Builder
	eng   *Engine
}

func newFixture(t *testing.T, response string) *fixture {
	t.Helper()
	ks, err := knowledge.NewFileStore(filepath.Join("..", "..", "content"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{ks: ks, store: newMemStore(), prov: &fakeProvider{text: response}, out: &strings.Builder{}}
	for _, c := range []struct{ id, name, class string }{{"aria", "Aria", "guerreiro"}, {"bruno", "Bruno", "mago"}} {
		cl, _ := ks.Class(c.class)
		if err := f.store.SaveCharacter(character.New(c.id, c.name, cl)); err != nil {
			t.Fatal(err)
		}
	}
	f.eng = f.open(t, OpenOptions{CampaignID: "cripta", CharacterIDs: []string{"aria", "bruno"}, EnvironmentID: "entrada-cripta"})
	return f
}

func (f *fixture) open(t *testing.T, opts OpenOptions) *Engine {
	t.Helper()
	eng, err := Open(Deps{Knowledge: f.ks, Campaigns: f.store, Characters: f.store, Provider: f.prov, Out: f.out},
		Config{MaxHPDelta: 10, HistorySize: 4, Now: func() time.Time { return testNow }}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

const mixedResponse = `[NARRATIVE]
Um goblin salta das sombras e arranha seu braço.
Você encontra moedas no chão.
[MECHANICS]
HP_CHANGE target=aria amount=-3
ITEM_GAINED target=aria item=moeda-ouro qty=5
ITEM_LOST target=aria item=moeda-ouro qty=2
HP_CHANGE target=goblin amount=-4
ITEM_GAINED target=aria item=excalibur
ITEM_LOST target=bruno item=pocao-cura
HP_CHANGE target=bruno amount=-50
HP_CHANGE target=aria
`

func TestPlayTurnValidatesAndApplies(t *testing.T) {
	f := newFixture(t, mixedResponse)
	res, err := f.eng.PlayTurn(context.Background(), "aria", "  abro a porta  ")
	if err != nil {
		t.Fatal(err)
	}

	if res.Narrative != "Um goblin salta das sombras e arranha seu braço.\nVocê encontra moedas no chão." {
		t.Errorf("narrativa %q", res.Narrative)
	}
	if len(res.Applied) != 3 || res.Applied[0].HPDelta != -3 {
		t.Errorf("aplicados %+v", res.Applied)
	}
	wantReasons := []string{"goblin", "excalibur", "insuficientes", "limite de 10"}
	if len(res.Rejected) != len(wantReasons) {
		t.Fatalf("rejeitados %+v", res.Rejected)
	}
	for i, w := range wantReasons {
		if !strings.Contains(res.Rejected[i].Reason, w) {
			t.Errorf("rejeitado %d: motivo %q sem %q", i, res.Rejected[i].Reason, w)
		}
	}
	if len(res.ParseErrors) != 1 {
		t.Errorf("parse errors %v", res.ParseErrors)
	}

	// A seção [MECHANICS] não vaza para o jogador.
	out := f.out.String()
	if !strings.Contains(out, "goblin salta") || strings.Contains(out, "[MECHANICS]") ||
		strings.Contains(out, "HP_CHANGE") || strings.Contains(out, "[NARRATIVE]") {
		t.Errorf("saída para o jogador:\n%s", out)
	}

	// Estado em memória.
	aria, _ := f.eng.Character("aria")
	bruno, _ := f.eng.Character("bruno")
	if aria.HP != 9 || aria.Inventory["moeda-ouro"] != 3 {
		t.Errorf("aria %+v", aria)
	}
	if bruno.HP != bruno.MaxHP {
		t.Errorf("bruno não deveria mudar: %+v", bruno)
	}

	// Persistência: personagem e log da sessão.
	saved, _ := f.store.LoadCharacter("aria")
	if saved.HP != 9 || saved.Inventory["moeda-ouro"] != 3 {
		t.Errorf("aria persistida %+v", saved)
	}
	camp, _ := f.store.LoadCampaign("cripta")
	sess, err := camp.ActiveSession()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range sess.Log {
		kinds[e.Kind]++
	}
	if kinds[KindAction] != 1 || kinds[KindNarration] != 1 || kinds[KindEffect] != 3 || kinds[KindEffectRejected] != 4 {
		t.Errorf("log %+v", sess.Log)
	}
	if sess.Log[0].Text != "abro a porta" || sess.Log[0].Actor != "aria" {
		t.Errorf("ação registrada %+v", sess.Log[0])
	}
}

func TestPlayTurnProviderFailureKeepsState(t *testing.T) {
	for name, prov := range map[string]*fakeProvider{
		"antes do stream": {startErr: llm.ErrNotConfigured},
		"no meio":         {text: mixedResponse, midErr: errors.New("conexão caiu")},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "")
			f.prov.text, f.prov.startErr, f.prov.midErr = prov.text, prov.startErr, prov.midErr
			savesBefore := f.store.saves
			campBefore := string(f.store.camps["cripta"])

			_, err := f.eng.PlayTurn(context.Background(), "aria", "ataco")
			if !errors.Is(err, ErrGeneration) {
				t.Fatalf("err = %v", err)
			}
			aria, _ := f.eng.Character("aria")
			if aria.HP != aria.MaxHP || aria.Inventory["moeda-ouro"] != 0 {
				t.Errorf("estado alterado: %+v", aria)
			}
			if n := len(f.eng.Session().Log); n != 0 {
				t.Errorf("log alterado: %d entradas", n)
			}
			if f.store.saves != savesBefore || string(f.store.camps["cripta"]) != campBefore {
				t.Error("nada deveria ser persistido")
			}
			if strings.Contains(f.out.String(), "HP_CHANGE") {
				t.Errorf("mecânica vazou: %q", f.out.String())
			}
		})
	}
}

func TestPlayTurnInputErrors(t *testing.T) {
	f := newFixture(t, mixedResponse)
	if _, err := f.eng.PlayTurn(context.Background(), "aria", "   "); !errors.Is(err, ErrEmptyInput) {
		t.Errorf("err = %v", err)
	}
	if _, err := f.eng.PlayTurn(context.Background(), "goblin", "oi"); !errors.Is(err, ErrUnknownActor) {
		t.Errorf("err = %v", err)
	}
}

func TestPromptContents(t *testing.T) {
	f := newFixture(t, "[NARRATIVE]\nok\n[MECHANICS]\n")
	ctx := context.Background()
	for i := range 3 {
		if _, err := f.eng.PlayTurn(ctx, "aria", fmt.Sprintf("ação %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.eng.PlayTurn(ctx, "bruno", "acendo a\ntocha"); err != nil {
		t.Fatal(err)
	}
	req := f.prov.last

	for _, want := range []string{"[NARRATIVE]", "[MECHANICS]", "HP_CHANGE target=", "ITEM_GAINED target=", "ITEM_LOST target=", "no máximo 10"} {
		if !strings.Contains(req.System, want) {
			t.Errorf("system sem %q", want)
		}
	}
	for _, want := range []string{
		"Entrada da cripta", "degraus escorregadios", "salao-ossos",
		"Aria (id aria, classe guerreiro) PV 12/12", "Espada curta [espada-curta] x1",
		"Bruno (id bruno, classe mago) PV 7/7", "moeda-ouro: Moeda de ouro",
		"[action] aria: ação 2",
	} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt sem %q:\n%s", want, req.Prompt)
		}
	}
	// HistorySize = 4: as entradas da primeira ação já saíram do prompt.
	if strings.Contains(req.Prompt, "ação 0") {
		t.Errorf("histórico deveria ter só as últimas 4 entradas:\n%s", req.Prompt)
	}
	lines := strings.Split(req.Prompt, "\n")
	if last := lines[len(lines)-1]; last != "Bruno (bruno): acendo a tocha" {
		t.Errorf("última linha %q", last)
	}
}

func TestOpenCampaign(t *testing.T) {
	f := newFixture(t, "")
	if !f.eng.Created || f.eng.Resumed || f.eng.Session().Number != 1 {
		t.Fatalf("created %v resumed %v", f.eng.Created, f.eng.Resumed)
	}

	// Reabrir retoma a sessão ativa, sem exigir personagens nem ambiente.
	again := f.open(t, OpenOptions{CampaignID: "cripta"})
	if again.Created || !again.Resumed || len(again.Party()) != 2 {
		t.Fatalf("reabertura: created %v resumed %v party %d", again.Created, again.Resumed, len(again.Party()))
	}

	// Encerrar e reabrir abre a sessão 2 no ambiente da anterior.
	if err := again.End(); err != nil {
		t.Fatal(err)
	}
	third := f.open(t, OpenOptions{CampaignID: "cripta"})
	if s := third.Session(); s == nil || s.Number != 2 || s.EnvironmentID != "entrada-cripta" {
		t.Fatalf("sessão %+v", s)
	}

	deps := Deps{Knowledge: f.ks, Campaigns: f.store, Characters: f.store, Provider: f.prov}
	for name, opts := range map[string]OpenOptions{
		"sem personagens":        {CampaignID: "nova", EnvironmentID: "entrada-cripta"},
		"sem ambiente":           {CampaignID: "nova", CharacterIDs: []string{"aria"}},
		"ambiente inexistente":   {CampaignID: "nova", CharacterIDs: []string{"aria"}, EnvironmentID: "lua"},
		"personagem inexistente": {CampaignID: "nova", CharacterIDs: []string{"zed"}, EnvironmentID: "entrada-cripta"},
	} {
		if _, err := Open(deps, Config{}, opts); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
	if _, err := f.store.LoadCampaign("nova"); err == nil {
		t.Error("campanha inválida não deveria ser persistida")
	}
}
