package events

import (
	"errors"
	"strings"
	"testing"
)

func TestPublishOrderAndErrors(t *testing.T) {
	d := NewDispatcher()
	var got []string
	d.Subscribe(PlayerAction, func(Event) error { got = append(got, "1"); return errors.New("x") })
	d.Subscribe(PlayerAction, func(Event) error { got = append(got, "2"); return nil })
	d.SubscribeAll(func(e Event) error { got = append(got, "all:"+string(e.Type)); return nil })
	d.Subscribe(NarrationDone, func(Event) error { got = append(got, "nope"); return nil })

	err := d.Publish(Event{Type: PlayerAction})
	if err == nil {
		t.Fatal("esperava erro agregado")
	}
	want := []string{"1", "2", "all:player.action"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func TestPublishRecoversHandlerPanic(t *testing.T) {
	d := NewDispatcher()
	var got []string
	d.Subscribe(SessionEnded, func(Event) error { got = append(got, "antes"); return nil })
	d.Subscribe(SessionEnded, func(Event) error { panic("boom") })
	d.Subscribe(SessionEnded, func(Event) error { got = append(got, "depois"); return errors.New("falhou") })

	err := d.Publish(Event{Type: SessionEnded})
	if !errors.Is(err, ErrHandlerPanic) {
		t.Fatalf("err = %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "boom") || !strings.Contains(msg, "session.ended") || !strings.Contains(msg, "falhou") {
		t.Errorf("mensagem sem contexto: %s", msg)
	}
	if len(got) != 2 || got[0] != "antes" || got[1] != "depois" {
		t.Errorf("handlers chamados: %v", got)
	}
}
