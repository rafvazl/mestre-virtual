package campaign

import (
	"errors"
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	c := New("c1", "A Cripta")
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)
	if _, err := c.ActiveSession(); !errors.Is(err, ErrNoActiveSession) {
		t.Fatal(err)
	}
	s, err := c.StartSession("entrada-cripta", now)
	if err != nil || s.Number != 1 {
		t.Fatalf("s %+v err %v", s, err)
	}
	if _, err := c.StartSession("x", now); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("err %v", err)
	}
	s.Append(LogEntry{Actor: "mestre", Kind: "narration", Text: "..."})
	if len(s.Log) != 1 || s.Log[0].At.IsZero() {
		t.Fatal("log")
	}
	if err := c.EndSession(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s2, _ := c.StartSession("salao-ossos", now); s2.Number != 2 {
		t.Fatal("numeração")
	}
	if _, err := c.StartSession("", now); err == nil {
		t.Fatal("ambiente vazio deveria falhar (e já há sessão ativa)")
	}
}

func TestAddCharacterIdempotent(t *testing.T) {
	c := New("c1", "x")
	c.AddCharacter("a")
	c.AddCharacter("a")
	if len(c.CharacterIDs) != 1 {
		t.Fatal(c.CharacterIDs)
	}
}
