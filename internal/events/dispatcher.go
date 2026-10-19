// Package events é um barramento de eventos in-process e síncrono.
//
// Não há broker (Kafka/NATS/SQS): a CLI é um único processo e a ordem dos
// eventos importa para a narrativa. Handlers rodam na goroutine de quem
// publica, na ordem de inscrição; um handler que falha — com erro ou com
// pânico — não impede os outros (ver docs/adr/0008).
package events

import (
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// Type identifica o tipo do evento.
type Type string

// Tipos de evento do domínio.
const (
	TurnPhaseChanged Type = "turn.phase_changed"
	PlayerAction     Type = "player.action"
	NarrationDone    Type = "narration.done"
	EffectApplied    Type = "effect.applied"
	EffectRejected   Type = "effect.rejected"
	SessionStarted   Type = "session.started"
	SessionEnded     Type = "session.ended"
)

// Event é uma ocorrência publicada no barramento.
type Event struct {
	Type    Type
	At      time.Time
	Payload any
}

// Handler trata um evento.
type Handler func(Event) error

// Dispatcher distribui eventos para handlers inscritos.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[Type][]Handler
	all      []Handler
}

// NewDispatcher cria um Dispatcher vazio.
func NewDispatcher() *Dispatcher { return &Dispatcher{handlers: map[Type][]Handler{}} }

// Subscribe inscreve h para eventos do tipo t.
func (d *Dispatcher) Subscribe(t Type, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[t] = append(d.handlers[t], h)
}

// SubscribeAll inscreve h para todos os eventos (ex.: log, memória).
func (d *Dispatcher) SubscribeAll(h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.all = append(d.all, h)
}

// ErrHandlerPanic indica que um handler entrou em pânico; Publish o converte
// em erro (com a pilha) em vez de derrubar quem publicou.
var ErrHandlerPanic = errors.New("events: pânico em handler")

// Publish entrega o evento a todos os handlers e agrega os erros. Um pânico
// num handler é recuperado e devolvido como erro que envolve ErrHandlerPanic;
// os handlers seguintes continuam recebendo o evento.
func (d *Dispatcher) Publish(e Event) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	d.mu.RLock()
	hs := make([]Handler, 0, len(d.handlers[e.Type])+len(d.all))
	hs = append(hs, d.handlers[e.Type]...)
	hs = append(hs, d.all...)
	d.mu.RUnlock()

	var errs []error
	for _, h := range hs {
		if err := safeCall(h, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// safeCall chama h convertendo um pânico em erro.
func safeCall(h Handler, e Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w (%s): %v\n%s", ErrHandlerPanic, e.Type, r, debug.Stack())
		}
	}()
	return h(e)
}
