package encounter

import (
	"errors"
	"testing"
)

func TestFullCycleAndRounds(t *testing.T) {
	m, err := NewTurnMachine([]string{"aria", "esqueleto"})
	if err != nil {
		t.Fatal(err)
	}
	var seen []Phase
	m.OnTransition(func(_, to Phase, _ string) { seen = append(seen, to) })
	for i := 0; i < 5; i++ {
		if _, err := m.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	if m.Phase() != AguardandoInput || m.Current() != "esqueleto" || m.Round() != 1 {
		t.Fatalf("fase %s atual %s rodada %d", m.Phase(), m.Current(), m.Round())
	}
	for i := 0; i < 5; i++ {
		m.Advance()
	}
	if m.Current() != "aria" || m.Round() != 2 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	want := []Phase{Resolvendo, Narrando, AplicandoEfeitos, ProximoTurno, AguardandoInput}
	for i, p := range want {
		if seen[i] != p {
			t.Fatalf("seen %v", seen)
		}
	}
}

func TestInvalidTransition(t *testing.T) {
	m, _ := NewTurnMachine([]string{"a"})
	if _, err := m.To(Narrando); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal(err)
	}
	if m.Phase() != AguardandoInput {
		t.Fatal("fase não deveria mudar")
	}
}

func TestNoParticipants(t *testing.T) {
	if _, err := NewTurnMachine(nil); !errors.Is(err, ErrNoParticipants) {
		t.Fatal(err)
	}
}
