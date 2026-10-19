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

// cycle executa um turno completo (5 transições).
func cycle(t *testing.T, m *TurnMachine) {
	t.Helper()
	for range 5 {
		if _, err := m.Advance(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSkipKeepsActorInOrder(t *testing.T) {
	m, _ := NewTurnMachine([]string{"aria", "bruno", "caio"})
	down := map[string]bool{"aria": true, "bruno": true}
	m.SetSkip(func(id string) bool { return down[id] })
	if m.Current() != "caio" || m.Round() != 1 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	down["aria"] = false // curada
	cycle(t, m)
	if m.Current() != "aria" || m.Round() != 2 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	cycle(t, m) // bruno continua com PV 0
	if m.Current() != "caio" || m.Round() != 2 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	if got := m.Order(); len(got) != 3 {
		t.Fatalf("ordem %v", got)
	}

	// Todos fora: a vez passa normalmente ao próximo, sem rodadas extras.
	down["aria"], down["caio"] = true, true
	cycle(t, m)
	if m.Current() != "aria" || m.Round() != 3 || m.Phase() != AguardandoInput {
		t.Fatalf("atual %s rodada %d fase %s", m.Current(), m.Round(), m.Phase())
	}
}

func TestRestore(t *testing.T) {
	m, _ := NewTurnMachine([]string{"aria", "bruno"})
	if err := m.Restore("bruno", 3); err != nil {
		t.Fatal(err)
	}
	if m.Current() != "bruno" || m.Round() != 3 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	cycle(t, m)
	if m.Current() != "aria" || m.Round() != 4 {
		t.Fatalf("atual %s rodada %d", m.Current(), m.Round())
	}
	if err := m.Restore("zed", 1); !errors.Is(err, ErrUnknownActor) {
		t.Fatalf("err = %v", err)
	}
	m.SetSkip(func(id string) bool { return id == "aria" })
	if err := m.Restore("aria", 0); err != nil || m.Current() != "bruno" || m.Round() != 1 {
		t.Fatalf("err %v atual %s rodada %d", err, m.Current(), m.Round())
	}
	m.Advance()
	if err := m.Restore("aria", 1); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("restaurar fora de AguardandoInput: %v", err)
	}
}

func TestAbortAndPass(t *testing.T) {
	m, _ := NewTurnMachine([]string{"aria", "bruno"})
	var seen []string
	m.OnTransition(func(from, to Phase, actor string) { seen = append(seen, from.String()+">"+to.String()+":"+actor) })

	if err := m.Abort(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("abortar em AguardandoInput: %v", err)
	}
	m.Advance()
	m.Advance() // Narrando
	if err := m.Abort(); err != nil {
		t.Fatal(err)
	}
	if m.Phase() != AguardandoInput || m.Current() != "aria" {
		t.Fatalf("fase %s atual %s", m.Phase(), m.Current())
	}

	if err := m.Pass(); err != nil {
		t.Fatal(err)
	}
	if err := m.Pass(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("passar em ProximoTurno: %v", err)
	}
	m.Advance()
	if m.Current() != "bruno" || m.Phase() != AguardandoInput {
		t.Fatalf("fase %s atual %s", m.Phase(), m.Current())
	}
	m.Advance()
	m.Advance()
	m.Advance() // AplicandoEfeitos
	if err := m.Abort(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("abortar depois de aplicar: %v", err)
	}
	want := "Narrando>AguardandoInput:aria"
	if seen[2] != want || seen[3] != "AguardandoInput>ProximoTurno:aria" || seen[4] != "ProximoTurno>AguardandoInput:bruno" {
		t.Fatalf("hooks %v", seen)
	}
}
