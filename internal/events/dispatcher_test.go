package events

import (
	"errors"
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
