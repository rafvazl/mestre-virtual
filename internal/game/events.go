package game

import "github.com/rafvazl/mestre-virtual/internal/encounter"

// Payloads dos eventos publicados pelo Engine:
//
//	events.SessionStarted    SessionInfo
//	events.TurnPhaseChanged  PhaseChange
//	events.PlayerAction      campaign.LogEntry (kind "action" ou "pass")
//	events.NarrationDone     campaign.LogEntry (kind "narration")
//	events.EffectApplied     campaign.LogEntry (kind "effect")
//	events.EffectRejected    campaign.LogEntry (kind "effect_rejected")
//	events.SessionEnded      SessionInfo
//
// campaign.LogEntry é a mesma entrada gravada em Session.Log e o formato que
// memory.EventRecorder grava na memória de aventura.

// PhaseChange é o payload de events.TurnPhaseChanged.
type PhaseChange struct {
	From, To encounter.Phase
	// Actor é o personagem da vez depois da transição (em
	// ProximoTurno → AguardandoInput, já é o próximo da iniciativa).
	Actor string
	Round int
}

// SessionInfo é o payload de events.SessionStarted e events.SessionEnded.
type SessionInfo struct {
	CampaignID    string
	Number        int
	EnvironmentID string
	// Resumed indica que a sessão já existia ao abrir o Engine.
	Resumed bool
}
