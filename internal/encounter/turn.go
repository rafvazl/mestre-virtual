// Package encounter contém a máquina de turno de um encontro.
//
// A máquina é fixa e independente do LLM: o LLM só produz texto e propõe
// efeitos; quem decide em que fase o turno está, e quando os efeitos são
// aplicados, é este código.
//
//	AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno → AguardandoInput
//
// Além do ciclo normal há duas transições de exceção, explícitas:
//
//	Abort: Resolvendo|Narrando → AguardandoInput (turno desfeito, mesmo ator)
//	Pass:  AguardandoInput → ProximoTurno       (ator passa a vez sem agir)
package encounter

import (
	"errors"
	"fmt"
)

// Phase é uma fase do turno.
type Phase int

const (
	AguardandoInput Phase = iota
	Resolvendo
	Narrando
	AplicandoEfeitos
	ProximoTurno
)

func (p Phase) String() string {
	switch p {
	case AguardandoInput:
		return "AguardandoInput"
	case Resolvendo:
		return "Resolvendo"
	case Narrando:
		return "Narrando"
	case AplicandoEfeitos:
		return "AplicandoEfeitos"
	case ProximoTurno:
		return "ProximoTurno"
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// next define a única transição válida a partir de cada fase.
var next = map[Phase]Phase{
	AguardandoInput:  Resolvendo,
	Resolvendo:       Narrando,
	Narrando:         AplicandoEfeitos,
	AplicandoEfeitos: ProximoTurno,
	ProximoTurno:     AguardandoInput,
}

// ErrInvalidTransition indica tentativa de pular/voltar fase.
var ErrInvalidTransition = errors.New("encounter: transição inválida")

// ErrNoParticipants indica encontro sem participantes.
var ErrNoParticipants = errors.New("encounter: sem participantes")

// TransitionHook é chamado após cada transição bem-sucedida.
type TransitionHook func(from, to Phase, actor string)

// ErrUnknownActor indica ator fora da ordem de iniciativa.
var ErrUnknownActor = errors.New("encounter: ator fora da iniciativa")

// TurnMachine controla fases e ordem de iniciativa.
type TurnMachine struct {
	order []string // IDs na ordem de iniciativa
	idx   int
	round int
	phase Phase
	hooks []TransitionHook
	skip  func(actor string) bool
}

// NewTurnMachine cria a máquina com a ordem de iniciativa dada.
func NewTurnMachine(order []string) (*TurnMachine, error) {
	if len(order) == 0 {
		return nil, ErrNoParticipants
	}
	cp := append([]string(nil), order...)
	return &TurnMachine{order: cp, round: 1, phase: AguardandoInput}, nil
}

// OnTransition registra um hook (ex.: publicar evento).
func (m *TurnMachine) OnTransition(h TransitionHook) { m.hooks = append(m.hooks, h) }

// Phase devolve a fase atual.
func (m *TurnMachine) Phase() Phase { return m.phase }

// Current devolve o ator do turno atual.
func (m *TurnMachine) Current() string { return m.order[m.idx] }

// Round devolve a rodada atual (começa em 1).
func (m *TurnMachine) Round() int { return m.round }

// Advance move para a próxima fase. Ao sair de ProximoTurno, passa a vez
// para o próximo da iniciativa (e incrementa a rodada ao dar a volta).
func (m *TurnMachine) Advance() (Phase, error) {
	return m.To(next[m.phase])
}

// To transiciona explicitamente para target, validando que é a próxima fase.
func (m *TurnMachine) To(target Phase) (Phase, error) {
	from := m.phase
	if next[from] != target {
		return from, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, target)
	}
	if from == ProximoTurno {
		m.step()
		m.settle()
	}
	m.set(from, target)
	return target, nil
}

// Abort desfaz o turno em andamento (ex.: o LLM falhou ao narrar) e volta a
// AguardandoInput com o mesmo ator. Só vale antes de AplicandoEfeitos:
// depois disso o estado do jogo já pode ter mudado.
func (m *TurnMachine) Abort() error {
	from := m.phase
	if from != Resolvendo && from != Narrando {
		return fmt.Errorf("%w: abortar a partir de %s", ErrInvalidTransition, from)
	}
	m.set(from, AguardandoInput)
	return nil
}

// Pass leva de AguardandoInput direto a ProximoTurno: o ator passa a vez
// sem agir. Quem chama completa o ciclo com Advance.
func (m *TurnMachine) Pass() error {
	if m.phase != AguardandoInput {
		return fmt.Errorf("%w: passar a vez a partir de %s", ErrInvalidTransition, m.phase)
	}
	m.set(AguardandoInput, ProximoTurno)
	return nil
}

// SetSkip define quem perde a vez (ex.: personagem com PV 0). O ator pulado
// continua na ordem de iniciativa e volta a jogar quando skip deixar de
// valer para ele. Se o ator atual já deve ser pulado (em AguardandoInput), a
// vez avança imediatamente, sem disparar hooks.
func (m *TurnMachine) SetSkip(skip func(actor string) bool) {
	m.skip = skip
	if m.phase == AguardandoInput {
		m.settle()
	}
}

// Restore reposiciona a iniciativa em actor e round, para retomar uma sessão
// salva. Só vale em AguardandoInput; round < 1 vira 1. Não dispara hooks.
func (m *TurnMachine) Restore(actor string, round int) error {
	if m.phase != AguardandoInput {
		return fmt.Errorf("%w: restaurar a partir de %s", ErrInvalidTransition, m.phase)
	}
	i := m.index(actor)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrUnknownActor, actor)
	}
	m.idx, m.round = i, max(round, 1)
	m.settle()
	return nil
}

func (m *TurnMachine) set(from, to Phase) {
	m.phase = to
	for _, h := range m.hooks {
		h(from, to, m.Current())
	}
}

// step passa a vez ao próximo da ordem, incrementando a rodada na volta.
func (m *TurnMachine) step() {
	m.idx++
	if m.idx >= len(m.order) {
		m.idx = 0
		m.round++
	}
}

// settle pula atores marcados por skip. Se todos estiverem marcados, fica
// onde começou, sem mexer na rodada (não há ninguém melhor para jogar).
func (m *TurnMachine) settle() {
	if m.skip == nil {
		return
	}
	idx, round := m.idx, m.round
	for range m.order {
		if !m.skip(m.Current()) {
			return
		}
		m.step()
	}
	m.idx, m.round = idx, round
}

func (m *TurnMachine) index(actor string) int {
	for i, id := range m.order {
		if id == actor {
			return i
		}
	}
	return -1
}

// Order devolve uma cópia da ordem de iniciativa.
func (m *TurnMachine) Order() []string { return append([]string(nil), m.order...) }
