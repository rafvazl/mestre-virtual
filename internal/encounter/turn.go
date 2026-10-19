// Package encounter contém a máquina de turno de um encontro.
//
// A máquina é fixa e independente do LLM: o LLM só produz texto e propõe
// efeitos; quem decide em que fase o turno está, e quando os efeitos são
// aplicados, é este código.
//
//	AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno → AguardandoInput
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

// TurnMachine controla fases e ordem de iniciativa.
type TurnMachine struct {
	order []string // IDs na ordem de iniciativa
	idx   int
	round int
	phase Phase
	hooks []TransitionHook
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
		m.idx++
		if m.idx >= len(m.order) {
			m.idx = 0
			m.round++
		}
	}
	m.phase = target
	for _, h := range m.hooks {
		h(from, target, m.Current())
	}
	return target, nil
}

// Order devolve uma cópia da ordem de iniciativa.
func (m *TurnMachine) Order() []string { return append([]string(nil), m.order...) }
