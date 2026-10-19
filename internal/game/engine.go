// Package game é o loop de narração: monta o prompt do turno, transmite a
// narrativa do LLM ao jogador e valida/aplica os efeitos propostos.
//
// Cada turno percorre as fases fixas de encounter.TurnMachine; a vez é de
// TurnMachine.Current(), na ordem de iniciativa da campanha. O que acontece
// é publicado num events.Dispatcher (a memória de aventura escuta por ali).
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
	"log/slog"
	"strings"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/encounter"
	"github.com/rafvazl/mestre-virtual/internal/events"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/memory"
	"github.com/rafvazl/mestre-virtual/internal/obs"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

// Atores e tipos de entrada usados no log da sessão.
const (
	ActorMaster = "mestre"

	KindAction         = "action"
	KindPass           = "pass"
	KindNarration      = "narration"
	KindEffect         = "effect"
	KindEffectRejected = "effect_rejected"
)

// Erros do loop de jogo.
var (
	ErrEmptyInput     = errors.New("game: ação vazia")
	ErrGeneration     = errors.New("game: falha na geração da narrativa")
	ErrTurnInProgress = errors.New("game: turno em andamento")
)

// Deps são as dependências injetadas no Engine.
type Deps struct {
	Knowledge  knowledge.Store
	Campaigns  storage.CampaignStore
	Characters storage.CharacterStore
	Provider   llm.Provider
	// Out recebe a narrativa em streaming.
	Out io.Writer

	// Events recebe os eventos do jogo; nil = um Dispatcher próprio. Use um
	// Dispatcher por Engine: Open inscreve nele o gravador da memória.
	Events *events.Dispatcher
	// Memory é a memória de aventura (opcional). Com ela, o prompt ganha a
	// seção "Memória da aventura" e os eventos são gravados automaticamente.
	Memory memory.Memory
	// Compactor (opcional, só com Memory) resume a sessão em SessionEnded.
	Compactor *memory.Compactor
	// Logger registra falhas de handlers e da memória e efeitos recusados;
	// nil = descarta.
	Logger *slog.Logger
}

// Config ajusta o comportamento do Engine. Zeros assumem os padrões.
type Config struct {
	// HistorySize é quantas entradas recentes do log entram no prompt (padrão 12).
	HistorySize int
	// MaxHPDelta é a magnitude máxima aceita para um HP_CHANGE (padrão 20).
	MaxHPDelta int
	// MemoryBudget é o orçamento, em tokens estimados, do bloco de memória
	// no prompt (padrão 400; só vale com Deps.Memory).
	MemoryBudget int
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
	if c.MemoryBudget <= 0 {
		c.MemoryBudget = 400
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
	party   []*character.Character // na ordem de camp.CharacterIDs (iniciativa)
	byID    map[string]*character.Character
	tm      *encounter.TurnMachine
	bus     *events.Dispatcher
	log     *slog.Logger
	mem     *memory.ContextBuilder // nil = sem memória
	refs    refIndex
	sessNum int  // número da sessão aberta (o gravador da memória precisa dele após End)
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
	if deps.Events == nil {
		deps.Events = events.NewDispatcher()
	}
	if deps.Logger == nil {
		deps.Logger = obs.Discard()
	}
	e := &Engine{deps: deps, cfg: cfg.withDefaults(), byID: map[string]*character.Character{},
		bus: deps.Events, log: deps.Logger}
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
	e.sessNum = sess.Number
	e.refs = newRefIndex(deps.Knowledge)
	e.setupTurns(sess)
	if err := deps.Campaigns.SaveCampaign(camp); err != nil {
		return nil, err
	}
	e.setupMemory()
	e.publish(events.SessionStarted, e.sessionInfo(sess))
	return e, nil
}

// setupTurns monta a máquina de turno na ordem dos personagens da campanha,
// pulando quem está com PV 0, e retoma a posição salva na sessão.
func (e *Engine) setupTurns(sess *campaign.Session) {
	tm, _ := encounter.NewTurnMachine(e.camp.CharacterIDs) // não vazio: validado em Open
	tm.SetSkip(func(id string) bool { return !e.byID[id].Alive() })
	if t := sess.Turn; t != nil {
		if err := tm.Restore(t.Actor, t.Round); err != nil {
			e.log.Warn("game: posição de turno salva inválida; recomeçando a iniciativa", "error", err)
		}
	}
	tm.OnTransition(func(from, to encounter.Phase, actor string) {
		e.publish(events.TurnPhaseChanged, PhaseChange{From: from, To: to, Actor: actor, Round: tm.Round()})
	})
	e.tm = tm
	e.saveTurn(sess)
}

// setupMemory liga a memória de aventura, se houver: o ContextBuilder para
// o prompt e o EventRecorder inscrito no Dispatcher.
func (e *Engine) setupMemory() {
	if e.deps.Memory == nil {
		return
	}
	e.mem = &memory.ContextBuilder{Memory: e.deps.Memory, Header: memoryHeader}
	rec := &memory.EventRecorder{
		Memory:    e.deps.Memory,
		Locate:    func() (string, int) { return e.camp.ID, e.sessNum },
		Resolve:   e.refs.Find,
		Compactor: e.deps.Compactor,
	}
	e.bus.SubscribeAll(rec.Handle)
}

// saveTurn grava a posição da iniciativa na sessão (persistida no próximo save).
func (e *Engine) saveTurn(sess *campaign.Session) {
	sess.Turn = &campaign.TurnState{Actor: e.tm.Current(), Round: e.tm.Round()}
}

// publish entrega um evento. Falha de handler nunca interrompe o turno: é
// só registrada no log.
func (e *Engine) publish(t events.Type, payload any) {
	err := e.bus.Publish(events.Event{Type: t, At: e.cfg.Now().UTC(), Payload: payload})
	if err != nil {
		e.log.Warn("game: falha em handler de evento", "event", string(t), "error", err)
	}
}

// record grava uma entrada no log da sessão e publica o evento
// correspondente, com a própria entrada como payload (formato que
// memory.EventRecorder entende).
func (e *Engine) record(sess *campaign.Session, t events.Type, entry campaign.LogEntry) {
	sess.Append(entry)
	e.publish(t, entry)
}

func (e *Engine) sessionInfo(s *campaign.Session) SessionInfo {
	return SessionInfo{CampaignID: e.camp.ID, Number: s.Number, EnvironmentID: s.EnvironmentID, Resumed: e.Resumed}
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

// Current devolve o ID do personagem da vez.
func (e *Engine) Current() string { return e.tm.Current() }

// Round devolve a rodada atual (começa em 1 a cada sessão).
func (e *Engine) Round() int { return e.tm.Round() }

// Phase devolve a fase atual da máquina de turno.
func (e *Engine) Phase() encounter.Phase { return e.tm.Phase() }

// Events devolve o Dispatcher em que o Engine publica.
func (e *Engine) Events() *events.Dispatcher { return e.bus }

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

// End encerra a sessão ativa, persiste a campanha e publica SessionEnded
// (que dispara a compactação da memória, se configurada).
func (e *Engine) End() error {
	sess, err := e.camp.ActiveSession()
	if err != nil {
		return err
	}
	if err := e.camp.EndSession(e.cfg.Now()); err != nil {
		return err
	}
	if err := e.deps.Campaigns.SaveCampaign(e.camp); err != nil {
		return err
	}
	e.publish(events.SessionEnded, e.sessionInfo(sess))
	return nil
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
type TurnResult struct {
	Actor       string
	Round       int
	Input       string
	Narrative   string
	Applied     []AppliedEffect
	Rejected    []RejectedEffect
	ParseErrors []error
}

// turn é o estado de trabalho de um turno, passado entre as fases.
type turn struct {
	res     TurnResult
	actor   *character.Character
	sess    *campaign.Session
	now     time.Time
	touched map[string]bool
}

// PlayTurn executa o turno do personagem da vez (Current), percorrendo as
// fases da TurnMachine:
//
//	AguardandoInput → Resolvendo      begin: valida a entrada
//	Resolvendo → Narrando             resolve: monta o prompt (com memória)
//	Narrando → AplicandoEfeitos       narrate + commitNarration: stream e log
//	AplicandoEfeitos → ProximoTurno   applyEffects: valida e aplica efeitos
//	ProximoTurno → AguardandoInput    finish: passa a vez e persiste
//
// Se a geração falhar (inclusive por cancelamento de ctx), o turno é
// abortado: a vez continua com o mesmo personagem, nada é aplicado, gravado
// no log ou publicado (exceto as mudanças de fase), e o erro envolve
// ErrGeneration. Um erro de persistência é devolvido junto com o resultado
// (o estado em memória já reflete o turno).
func (e *Engine) PlayTurn(ctx context.Context, input string) (TurnResult, error) {
	t, err := e.begin(input)
	if err != nil {
		return t.res, err
	}
	req, err := e.resolve(ctx, t)
	if err != nil {
		e.abort()
		return t.res, err
	}
	raw, err := e.narrate(ctx, req)
	if err != nil {
		e.abort()
		return t.res, err
	}
	effects := e.commitNarration(t, raw)
	e.applyEffects(t, effects)
	return t.res, e.finish(t)
}

// begin valida a entrada e abre o turno (AguardandoInput → Resolvendo).
func (e *Engine) begin(input string) (*turn, error) {
	t := &turn{res: TurnResult{Actor: e.tm.Current(), Round: e.tm.Round(), Input: strings.TrimSpace(input)},
		touched: map[string]bool{}}
	if t.res.Input == "" {
		return t, ErrEmptyInput
	}
	if p := e.tm.Phase(); p != encounter.AguardandoInput {
		return t, fmt.Errorf("%w (fase %s)", ErrTurnInProgress, p)
	}
	sess, err := e.camp.ActiveSession()
	if err != nil {
		return t, err
	}
	t.sess, t.actor = sess, e.byID[t.res.Actor]
	return t, e.to(encounter.Resolvendo)
}

// resolve monta a requisição ao LLM (Resolvendo → Narrando).
func (e *Engine) resolve(ctx context.Context, t *turn) (llm.Request, error) {
	env, err := e.deps.Knowledge.Environment(t.sess.EnvironmentID)
	if err != nil {
		return llm.Request{}, err
	}
	req := llm.Request{
		System: buildSystem(e.cfg.MaxHPDelta),
		Prompt: buildPrompt(e.deps.Knowledge, promptData{
			env:     env,
			party:   e.party,
			memory:  e.recall(ctx, t.sess, env, t.res.Input),
			history: e.history(t.sess),
			actor:   t.actor,
			input:   t.res.Input,
		}),
		Tags: map[string]string{"kind": "narration"},
	}
	return req, e.to(encounter.Narrando)
}

// recall monta o bloco de memória para o turno. Falha da memória não impede
// o turno: o prompt só sai sem a seção.
func (e *Engine) recall(ctx context.Context, sess *campaign.Session, env knowledge.Environment, input string) string {
	if e.mem == nil {
		return ""
	}
	refs := append([]knowledge.Ref{{Type: knowledge.RefEnvironment, ID: env.ID}}, e.refs.Find(input)...)
	q := memory.Query{CampaignID: e.camp.ID, Text: input, Refs: refs, CurrentSession: sess.Number}
	blk, err := e.mem.Build(ctx, q, e.cfg.MemoryBudget)
	if err != nil {
		e.log.Warn("game: memória indisponível neste turno", "error", err)
		return ""
	}
	e.log.Debug("game: memória no prompt", "entries", len(blk.Entries), "tokens", blk.Tokens)
	return blk.Text
}

// narrate consome o stream do LLM (fase Narrando).
func (e *Engine) narrate(ctx context.Context, req llm.Request) (string, error) {
	return e.generate(ctx, req)
}

// commitNarration interpreta a resposta, registra ação e narração no log
// (publicando PlayerAction e NarrationDone) e avança para AplicandoEfeitos.
//
// Os eventos de log só saem aqui, com a narração confirmada: assim o log da
// sessão e a memória nunca divergem quando a geração falha.
func (e *Engine) commitNarration(t *turn, raw string) []llm.StructuredEffect {
	parsed, perrs := llm.ParseResponse(raw)
	t.res.Narrative = parsed.Narrative
	t.res.ParseErrors = perrs
	for _, pe := range perrs {
		e.log.Info("game: linha de mecânica ignorada", "error", pe)
	}

	t.now = e.cfg.Now().UTC()
	e.record(t.sess, events.PlayerAction, campaign.LogEntry{At: t.now, Actor: t.res.Actor, Kind: KindAction, Text: t.res.Input})
	if t.res.Narrative != "" {
		e.record(t.sess, events.NarrationDone, campaign.LogEntry{At: t.now, Actor: ActorMaster, Kind: KindNarration, Text: t.res.Narrative})
	}
	e.mustTo(encounter.AplicandoEfeitos)
	return parsed.Effects
}

// applyEffects valida e aplica os efeitos propostos (fase AplicandoEfeitos),
// publicando EffectApplied ou EffectRejected para cada um.
func (e *Engine) applyEffects(t *turn, effects []llm.StructuredEffect) {
	for _, eff := range effects {
		applied, reason := e.apply(eff)
		if reason != "" {
			t.res.Rejected = append(t.res.Rejected, RejectedEffect{Effect: eff, Reason: reason})
			e.log.Info("game: efeito recusado", "effect", FormatEffect(eff), "reason", reason)
			e.record(t.sess, events.EffectRejected, campaign.LogEntry{At: t.now, Actor: ActorMaster, Kind: KindEffectRejected,
				Text: fmt.Sprintf("%s (motivo: %s)", FormatEffect(eff), reason)})
			continue
		}
		t.res.Applied = append(t.res.Applied, applied)
		t.touched[eff.Target] = true
		e.record(t.sess, events.EffectApplied, campaign.LogEntry{At: t.now, Actor: eff.Target, Kind: KindEffect, Text: e.DescribeApplied(applied)})
	}
}

// finish fecha o turno (AplicandoEfeitos → ProximoTurno → AguardandoInput),
// passando a vez ao próximo da iniciativa, e persiste.
func (e *Engine) finish(t *turn) error {
	e.mustTo(encounter.ProximoTurno)
	e.mustTo(encounter.AguardandoInput)
	e.saveTurn(t.sess)
	return e.persist(t.touched)
}

// Pass faz o personagem da vez passar sem agir: registra a passagem no log
// (publicando PlayerAction com kind "pass"), leva a máquina de
// AguardandoInput a ProximoTurno e dali ao próximo da iniciativa. Devolve o
// ID de quem passou.
func (e *Engine) Pass() (string, error) {
	actor := e.tm.Current()
	if p := e.tm.Phase(); p != encounter.AguardandoInput {
		return actor, fmt.Errorf("%w (fase %s)", ErrTurnInProgress, p)
	}
	sess, err := e.camp.ActiveSession()
	if err != nil {
		return actor, err
	}
	e.record(sess, events.PlayerAction, campaign.LogEntry{At: e.cfg.Now().UTC(), Actor: actor, Kind: KindPass, Text: "passa a vez"})
	if err := e.tm.Pass(); err != nil {
		return actor, err
	}
	e.mustTo(encounter.AguardandoInput)
	e.saveTurn(sess)
	return actor, e.persist(nil)
}

// to avança a máquina para a fase indicada.
func (e *Engine) to(p encounter.Phase) error {
	_, err := e.tm.To(p)
	return err
}

// mustTo avança para uma fase que o próprio Engine garante ser a próxima;
// falhar aqui é bug de programação.
func (e *Engine) mustTo(p encounter.Phase) {
	if err := e.to(p); err != nil {
		panic(err)
	}
}

// abort desfaz o turno em andamento; a vez continua com o mesmo personagem.
func (e *Engine) abort() {
	if err := e.tm.Abort(); err != nil {
		e.log.Error("game: não foi possível abortar o turno", "error", err)
	}
}

// generate consome o stream, repassando a narrativa ao jogador, e devolve o
// texto bruto completo.
//
// Segue o contrato de cancelamento de llm.Provider.Stream: o canal é sempre
// drenado até fechar (para não prender a goroutine do provider) e, ao fim,
// ctx.Err() é checado — um stream que fechou "limpo" com o contexto
// cancelado está truncado e conta como falha de geração. Depois do
// cancelamento, nada mais é escrito para o jogador.
func (e *Engine) generate(ctx context.Context, req llm.Request) (string, error) {
	ch, err := e.deps.Provider.Stream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("%w (%s): %w", ErrGeneration, e.deps.Provider.Name(), err)
	}
	filter := newNarrationFilter(e.deps.Out)
	var raw strings.Builder
	var streamErr error
	for c := range ch {
		if streamErr != nil || ctx.Err() != nil {
			continue // drena
		}
		if c.Err != nil {
			streamErr = c.Err
			continue
		}
		raw.WriteString(c.Text)
		filter.Write(c.Text)
	}
	if ctx.Err() != nil {
		filter.dropPending()
	}
	filter.Flush()
	if streamErr != nil {
		return "", fmt.Errorf("%w (%s): %w", ErrGeneration, e.deps.Provider.Name(), streamErr)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w (%s): %w", ErrGeneration, e.deps.Provider.Name(), err)
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
