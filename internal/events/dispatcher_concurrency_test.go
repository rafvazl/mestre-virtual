package events

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Publicação e inscrição concorrentes: precisa passar limpo sob -race.
func TestConcurrentPublishAndSubscribe(t *testing.T) {
	d := NewDispatcher()
	var typed, all atomic.Int64
	d.Subscribe(PlayerAction, func(Event) error { typed.Add(1); return nil })
	d.SubscribeAll(func(Event) error { all.Add(1); return nil })

	const publishers, perPublisher = 16, 200
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPublisher; j++ {
				if err := d.Publish(Event{Type: PlayerAction, Payload: j}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	// Inscrições concorrentes em outro tipo não podem interferir.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				d.Subscribe(NarrationDone, func(Event) error { return nil })
				d.SubscribeAll(func(Event) error { return nil })
			}
		}()
	}
	wg.Wait()
	if got := typed.Load(); got != publishers*perPublisher {
		t.Fatalf("handler tipado recebeu %d, quer %d", got, publishers*perPublisher)
	}
	if got := all.Load(); got != publishers*perPublisher {
		t.Fatalf("handler global recebeu %d, quer %d", got, publishers*perPublisher)
	}
}

// Um handler pode inscrever outro (ou publicar) sem deadlock: o lock é
// liberado antes de chamar os handlers. O novo handler vale só a partir da
// próxima publicação.
func TestHandlerCanSubscribeAndPublish(t *testing.T) {
	d := NewDispatcher()
	var late atomic.Int32
	d.Subscribe(SessionStarted, func(Event) error {
		d.Subscribe(SessionStarted, func(Event) error { late.Add(1); return nil })
		return d.Publish(Event{Type: SessionEnded})
	})
	done := make(chan error, 1)
	go func() { done <- d.Publish(Event{Type: SessionStarted}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock ao publicar de dentro de um handler")
	}
	if late.Load() != 0 {
		t.Fatal("handler inscrito durante a publicação não deveria rodar nela")
	}
	d.Publish(Event{Type: SessionStarted})
	if late.Load() != 1 {
		t.Fatalf("late = %d", late.Load())
	}
}

func TestPublishSetsTimestampAndAggregatesErrors(t *testing.T) {
	d := NewDispatcher()
	e1, e2 := errors.New("um"), errors.New("dois")
	var seen Event
	d.Subscribe(EffectApplied, func(e Event) error { seen = e; return e1 })
	d.SubscribeAll(func(Event) error { return e2 })
	err := d.Publish(Event{Type: EffectApplied})
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("err = %v", err)
	}
	if seen.At.IsZero() || seen.At.Location() != time.UTC {
		t.Fatalf("At = %v", seen.At)
	}
	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	d.Publish(Event{Type: EffectApplied, At: fixed})
	if !seen.At.Equal(fixed) {
		t.Fatalf("At explícito sobrescrito: %v", seen.At)
	}
}

func TestPublishWithoutHandlers(t *testing.T) {
	if err := NewDispatcher().Publish(Event{Type: "desconhecido"}); err != nil {
		t.Fatal(err)
	}
}
