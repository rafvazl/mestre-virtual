package memory

import (
	"context"
	"fmt"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/events"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

// EventRecorder grava na memória, automaticamente, o que passa pelo
// events.Dispatcher.
//
// Ligação prevista para a Fase 4 (loop de jogo + eventos):
//
//	rec := &memory.EventRecorder{
//		Memory:    mem,
//		Locate:    func() (string, int) { return camp.ID, sess.Number },
//		Resolve:   resolverDeRefs, // opcional: nomes da knowledge → Refs
//		Compactor: &memory.Compactor{Store: mem, Summarizer: memory.LLMSummarizer{Provider: p}},
//	}
//	dispatcher.SubscribeAll(rec.Handle)
//
// e publicar eventos com payload campaign.LogEntry (o mesmo que vai para
// Session.Log) ou memory.Entry (para fatos explícitos). Em SessionEnded a
// sessão é compactada se houver Compactor.
type EventRecorder struct {
	Memory Memory
	// Locate informa a campanha e a sessão correntes; LogEntry não as
	// carrega.
	Locate func() (campaignID string, session int)
	// Resolve, se presente, extrai Refs do texto (ex.: casando nomes da
	// base de conhecimento).
	Resolve func(text string) []knowledge.Ref
	// Compactor, se presente, é acionado em events.SessionEnded.
	Compactor *Compactor
}

// Handle é o events.Handler. Payloads desconhecidos são ignorados.
func (r *EventRecorder) Handle(ev events.Event) error {
	ctx := context.Background()
	if r.Locate == nil {
		return fmt.Errorf("memory: EventRecorder sem Locate")
	}
	campaignID, session := r.Locate()

	if ev.Type == events.SessionEnded && r.Compactor != nil {
		_, _, err := r.Compactor.CompactSession(ctx, campaignID, session)
		return err
	}

	var e Entry
	switch p := ev.Payload.(type) {
	case campaign.LogEntry:
		e = fromLog(p)
	case *campaign.LogEntry:
		if p == nil {
			return nil
		}
		e = fromLog(*p)
	case Entry:
		e = p
	case *Entry:
		if p == nil {
			return nil
		}
		e = *p
	default:
		return nil
	}
	if e.CampaignID == "" {
		e.CampaignID = campaignID
	}
	if e.SessionNumber == 0 {
		e.SessionNumber = session
	}
	if e.At.IsZero() {
		e.At = ev.At
	}
	if len(e.Refs) == 0 && r.Resolve != nil {
		e.Refs = r.Resolve(e.Text)
	}
	_, err := r.Memory.Record(ctx, e)
	return err
}

// fromLog converte uma linha do log da sessão. Efeitos mecânicos pesam um
// pouco mais (mudam o estado do mundo); kind "fact" vira KindFact.
func fromLog(l campaign.LogEntry) Entry {
	e := Entry{At: l.At, Actor: l.Actor, Text: l.Text, Kind: KindEvent}
	switch l.Kind {
	case "fact":
		e.Kind = KindFact
		e.Importance = 0.7
	case "effect":
		e.Importance = 0.6
	}
	return e
}
