// Package game é o loop de narração: monta o prompt do turno, transmite a
// narrativa do LLM ao jogador e valida/aplica os efeitos propostos.
//
// Regra de ouro: o LLM nunca altera estado. Ele propõe efeitos no contrato
// [NARRATIVE]/[MECHANICS]; este pacote valida cada efeito contra a campanha e
// a base de conhecimento e só então chama os métodos de character.
package game

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

// Atores e tipos de entrada usados no log da sessão.
const (
	ActorMaster = "mestre"

	KindAction         = "action"
	KindNarration      = "narration"
	KindEffect         = "effect"
	KindEffectRejected = "effect_rejected"
)

// Erros do loop de jogo.
var (
	ErrUnknownActor = errors.New("game: personagem não faz parte da campanha")
	ErrEmptyInput   = errors.New("game: ação vazia")
	ErrGeneration   = errors.New("game: falha na geração da narrativa")
)

// Deps são as dependências injetadas no Engine.
type Deps struct {
	Knowledge  knowledge.Store
	Campaigns  storage.CampaignStore
	Characters storage.CharacterStore
	Provider   llm.Provider
	// Out recebe a narrativa em streaming.
	Out io.Writer
}

// Config ajusta o comportamento do Engine. Zeros assumem os padrões.
type Config struct {
	// HistorySize é quantas entradas recentes do log entram no prompt (padrão 12).
	HistorySize int
	// MaxHPDelta é a magnitude máxima aceita para um HP_CHANGE (padrão 20).
	MaxHPDelta int
	// Now permite fixar o relógio em testes (padrão time.Now).
	Now func() time.Time
}

func (c Config) withDefaults() Config {
	if c.HistorySize <= 0 {
		c.HistorySize = 12
	}
	if c.MaxHPDelta <= 0 {
		c.MaxHPDelta = 20
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// OpenOptions identifica a campanha a jogar.
type OpenOptions struct {
	CampaignID string
	// CharacterIDs são obrigatórios ao criar a campanha; numa campanha
	// existente, IDs novos são vinculados a ela.
	CharacterIDs []string
	// EnvironmentID é obrigatório ao criar a campanha; numa existente, é
	// usado apenas se for preciso abrir uma nova sessão (o padrão é o
	// ambiente da última sessão).
	EnvironmentID string
}

// Engine conduz os turnos de uma sessão. Não é seguro para uso concorrente:
// a CLI processa uma ação por vez.
type Engine struct {
	deps    Deps
	cfg     Config
	camp    *campaign.Campaign
	party   []*character.Character // na ordem de camp.CharacterIDs
	byID    map[string]*character.Character
	Created bool // a campanha foi criada nesta abertura
	Resumed bool // a sessão ativa já existia
}

// Open carrega (ou cria) a campanha, carrega o grupo e garante uma sessão
// ativa, persistindo o que tiver mudado.
func Open(deps Deps, cfg Config, opts OpenOptions) (*Engine, error) {
	if deps.Knowledge == nil || deps.Campaigns == nil || deps.Characters == nil || deps.Provider == nil {
		return nil, errors.New("game: dependências incompletas")
	}
	if deps.Out == nil {
		deps.Out = io.Discard
	}
	e := &Engine{deps: deps, cfg: cfg.withDefaults(), byID: map[string]*character.Character{}}
	if opts.CampaignID == "" {
		return nil, errors.New("game: ID da campanha obrigatório")
	}

	camp, err := deps.Campaigns.LoadCampaign(opts.CampaignID)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		if len(opts.CharacterIDs) == 0 || opts.EnvironmentID == "" {
			return nil, fmt.Errorf("game: campanha %q não existe; para criá-la informe personagens e ambiente", opts.CampaignID)
		}
		camp = campaign.New(opts.CampaignID, opts.CampaignID)
		camp.CreatedAt = e.cfg.Now().UTC()
		e.Created = true
	case err != nil:
		return nil, err
	}
	for _, id := range opts.CharacterIDs {
		camp.AddCharacter(id)
	}
	if len(camp.CharacterIDs) == 0 {
		return nil, fmt.Errorf("game: campanha %q sem personagens", camp.ID)
	}
	for _, id := range camp.CharacterIDs {
		c, err := deps.Characters.LoadCharacter(id)
		if err != nil {
			return nil, fmt.Errorf("game: personagem %q: %w", id, err)
		}
		e.party = append(e.party, c)
		e.byID[id] = c
	}

	if _, err := camp.ActiveSession(); err == nil {
		e.Resumed = true
	} else {
		env := opts.EnvironmentID
		if env == "" && len(camp.Sessions) > 0 {
			env = camp.Sessions[len(camp.Sessions)-1].EnvironmentID
		}
		if env == "" {
			return nil, errors.New("game: informe o ambiente para abrir a sessão")
		}
		if _, err := camp.StartSession(env, e.cfg.Now()); err != nil {
			return nil, err
		}
	}
	sess, _ := camp.ActiveSession()
	if !deps.Knowledge.Exists(knowledge.Ref{Type: knowledge.RefEnvironment, ID: sess.EnvironmentID}) {
		return nil, fmt.Errorf("game: ambiente %q não existe na base de conhecimento", sess.EnvironmentID)
	}
	e.camp = camp
	if err := deps.Campaigns.SaveCampaign(camp); err != nil {
		return nil, err
	}
	return e, nil
}

// Campaign devolve a campanha em jogo.
func (e *Engine) Campaign() *campaign.Campaign { return e.camp }

// Session devolve a sessão ativa (nil se encerrada).
func (e *Engine) Session() *campaign.Session {
	s, err := e.camp.ActiveSession()
	if err != nil {
		return nil
	}
	return s
}

// Party devolve os personagens na ordem da campanha.
func (e *Engine) Party() []*character.Character { return e.party }

// Character devolve um personagem do grupo.
func (e *Engine) Character(id string) (*character.Character, bool) {
	c, ok := e.byID[id]
	return c, ok
}

// Environment devolve o ambiente da sessão ativa.
func (e *Engine) Environment() (knowledge.Environment, error) {
	s := e.Session()
	if s == nil {
		return knowledge.Environment{}, campaign.ErrNoActiveSession
	}
	return e.deps.Knowledge.Environment(s.EnvironmentID)
}

// WriteStatus escreve PV e inventário de cada personagem.
func (e *Engine) WriteStatus(w io.Writer) {
	for _, c := range e.party {
		fmt.Fprintf(w, "%s (%s, %s) PV %d/%d\n  inventário: %s\n",
			c.Name, c.ID, c.ClassID, c.HP, c.MaxHP, describeInventory(e.deps.Knowledge, c))
	}
}

// End encerra a sessão ativa e persiste a campanha.
func (e *Engine) End() error {
	if err := e.camp.EndSession(e.cfg.Now()); err != nil {
		return err
	}
	return e.deps.Campaigns.SaveCampaign(e.camp)
}

// AppliedEffect é um efeito aceito e aplicado.
type AppliedEffect struct {
	Effect llm.StructuredEffect
	// HPDelta é o delta efetivo de PV após o clamp (só HP_CHANGE).
	HPDelta int
}

// RejectedEffect é um efeito proposto pelo LLM e recusado pelo núcleo.
type RejectedEffect struct {
	Effect llm.StructuredEffect
	Reason string
}

// TurnResult descreve o que aconteceu num turno.
//
// Fase 4: este é o ponto de integração com encounter.TurnMachine e
// events.Dispatcher — cada campo corresponde a um evento (PlayerAction,
// NarrationDone, EffectApplied, EffectRejected) e a uma fase do turno.
type TurnResult struct {
	Actor       string
	Input       string
	Narrative   string
	Applied     []AppliedEffect
	Rejected    []RejectedEffect
	ParseErrors []error
}

// PlayTurn executa um turno: gera a narração em streaming para Out, valida e
// aplica os efeitos propostos, registra tudo no log e persiste.
//
// Se a geração falhar, nada é aplicado nem registrado e o erro envolve
// ErrGeneration. Um erro de persistência é devolvido junto com o resultado
// (o estado em memória já reflete o turno).
func (e *Engine) PlayTurn(ctx context.Context, actorID, input string) (TurnResult, error) {
	res := TurnResult{Actor: actorID, Input: strings.TrimSpace(input)}
	if res.Input == "" {
		return res, ErrEmptyInput
	}
	actor, ok := e.byID[actorID]
	if !ok {
		return res, fmt.Errorf("%w: %q", ErrUnknownActor, actorID)
	}
	sess, err := e.camp.ActiveSession()
	if err != nil {
		return res, err
	}
	env, err := e.deps.Knowledge.Environment(sess.EnvironmentID)
	if err != nil {
		return res, err
	}

	// Fase 4: TurnMachine AguardandoInput → Resolvendo; publicar PlayerAction.
	req := llm.Request{
		System: buildSystem(e.cfg.MaxHPDelta),
		Prompt: buildPrompt(e.deps.Knowledge, env, e.party, e.history(sess), actor, res.Input),
		Tags:   map[string]string{"kind": "narration"},
	}
	raw, err := e.generate(ctx, req)
	if err != nil {
		return res, err
	}

	// Fase 4: Narrando → AplicandoEfeitos; publicar NarrationDone.
	parsed, perrs := llm.ParseResponse(raw)
	res.Narrative = parsed.Narrative
	res.ParseErrors = perrs

	now := e.cfg.Now().UTC()
	sess.Append(campaign.LogEntry{At: now, Actor: actorID, Kind: KindAction, Text: res.Input})
	if res.Narrative != "" {
		sess.Append(campaign.LogEntry{At: now, Actor: ActorMaster, Kind: KindNarration, Text: res.Narrative})
	}
	touched := map[string]bool{}
	for _, eff := range parsed.Effects {
		// Fase 4: publicar EffectApplied / EffectRejected aqui.
		applied, reason := e.apply(eff)
		if reason != "" {
			res.Rejected = append(res.Rejected, RejectedEffect{Effect: eff, Reason: reason})
			sess.Append(campaign.LogEntry{At: now, Actor: ActorMaster, Kind: KindEffectRejected,
				Text: fmt.Sprintf("%s (motivo: %s)", FormatEffect(eff), reason)})
			continue
		}
		res.Applied = append(res.Applied, applied)
		touched[eff.Target] = true
		sess.Append(campaign.LogEntry{At: now, Actor: eff.Target, Kind: KindEffect, Text: e.DescribeApplied(applied)})
	}

	// Fase 4: AplicandoEfeitos → ProximoTurno.
	return res, e.persist(touched)
}

// generate consome o stream, repassando a narrativa ao jogador, e devolve o
// texto bruto completo.
func (e *Engine) generate(ctx context.Context, req llm.Request) (string, error) {
	ch, err := e.deps.Provider.Stream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("%w (%s): %w", ErrGeneration, e.deps.Provider.Name(), err)
	}
	filter := newNarrationFilter(e.deps.Out)
	var raw strings.Builder
	for c := range ch {
		if c.Err != nil {
			filter.Flush()
			// Drena o canal para não prender a goroutine do provider.
			for range ch {
			}
			return "", fmt.Errorf("%w (%s): %w", ErrGeneration, e.deps.Provider.Name(), c.Err)
		}
		raw.WriteString(c.Text)
		filter.Write(c.Text)
	}
	filter.Flush()
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrGeneration, err)
	}
	return raw.String(), nil
}

// apply valida e aplica um efeito. Devolve o motivo da recusa, ou "".
// Efeitos são aplicados em ordem, então a validação enxerga os anteriores
// do mesmo turno (ex.: ganhar e depois perder o mesmo item).
func (e *Engine) apply(eff llm.StructuredEffect) (AppliedEffect, string) {
	out := AppliedEffect{Effect: eff}
	c, ok := e.byID[eff.Target]
	if !ok {
		return out, fmt.Sprintf("alvo %q não é personagem da campanha", eff.Target)
	}
	switch eff.Type {
	case llm.EffectHPChange:
		if eff.Amount == 0 {
			return out, "HP_CHANGE com amount zero"
		}
		if eff.Amount > e.cfg.MaxHPDelta || eff.Amount < -e.cfg.MaxHPDelta {
			return out, fmt.Sprintf("|amount| %d excede o limite de %d", abs(eff.Amount), e.cfg.MaxHPDelta)
		}
		out.HPDelta = c.ApplyHPChange(eff.Amount)
	case llm.EffectItemGained, llm.EffectItemLost:
		if !e.deps.Knowledge.Exists(knowledge.Ref{Type: knowledge.RefItem, ID: eff.ItemID}) {
			return out, fmt.Sprintf("item %q não existe na base de conhecimento", eff.ItemID)
		}
		var err error
		if eff.Type == llm.EffectItemGained {
			err = c.AddItem(eff.ItemID, eff.Amount)
		} else {
			err = c.RemoveItem(eff.ItemID, eff.Amount)
		}
		if err != nil {
			return out, err.Error()
		}
	default:
		return out, fmt.Sprintf("tipo de efeito %q não suportado", eff.Type)
	}
	return out, ""
}

// persist salva os personagens alterados e depois a campanha (com o log).
func (e *Engine) persist(touched map[string]bool) error {
	var errs []error
	for _, c := range e.party {
		if touched[c.ID] {
			if err := e.deps.Characters.SaveCharacter(c); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := e.deps.Campaigns.SaveCampaign(e.camp); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("game: falha ao salvar o turno: %w", err)
	}
	return nil
}

// history devolve as últimas HistorySize entradas do log.
func (e *Engine) history(s *campaign.Session) []campaign.LogEntry {
	if n := len(s.Log); n > e.cfg.HistorySize {
		return s.Log[n-e.cfg.HistorySize:]
	}
	return s.Log
}

// DescribeApplied descreve um efeito aplicado (para o log e para o jogador).
func (e *Engine) DescribeApplied(a AppliedEffect) string {
	c := e.byID[a.Effect.Target]
	switch a.Effect.Type {
	case llm.EffectHPChange:
		return fmt.Sprintf("%s: PV %+d (agora %d/%d)", c.Name, a.HPDelta, c.HP, c.MaxHP)
	case llm.EffectItemGained:
		return fmt.Sprintf("%s ganhou %s x%d", c.Name, itemName(e.deps.Knowledge, a.Effect.ItemID), a.Effect.Amount)
	case llm.EffectItemLost:
		return fmt.Sprintf("%s perdeu %s x%d", c.Name, itemName(e.deps.Knowledge, a.Effect.ItemID), a.Effect.Amount)
	}
	return FormatEffect(a.Effect)
}

// FormatEffect reescreve um efeito na sintaxe do contrato [MECHANICS].
func FormatEffect(eff llm.StructuredEffect) string {
	if eff.Type == llm.EffectHPChange {
		return fmt.Sprintf("%s target=%s amount=%d", eff.Type, eff.Target, eff.Amount)
	}
	return fmt.Sprintf("%s target=%s item=%s qty=%d", eff.Type, eff.Target, eff.ItemID, eff.Amount)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
