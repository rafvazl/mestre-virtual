// Package campaign modela campanhas e sessões de jogo.
//
// Uma Campaign agrupa personagens e um histórico de Sessions. A Session é o
// estado de uma noite de jogo: onde o grupo está, quem participa e o log
// de eventos narrados (insumo da futura internal/memory).
package campaign

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoActiveSession indica que não há sessão aberta.
var ErrNoActiveSession = errors.New("campaign: nenhuma sessão ativa")

// ErrSessionActive indica que já existe uma sessão aberta.
var ErrSessionActive = errors.New("campaign: já existe sessão ativa")

// Campaign é uma campanha persistente.
type Campaign struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Setting      string     `json:"setting,omitempty"`
	CharacterIDs []string   `json:"character_ids"`
	Sessions     []*Session `json:"sessions"`
	CreatedAt    time.Time  `json:"created_at"`
}

// LogEntry é um registro do que aconteceu numa sessão.
type LogEntry struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"` // ID do personagem ou "mestre"
	Kind  string    `json:"kind"`  // "action", "narration", "effect"...
	Text  string    `json:"text"`
}

// TurnState é a posição da iniciativa salva entre turnos: de quem é a vez e
// em que rodada. Só é gravada com a máquina de turno em AguardandoInput.
type TurnState struct {
	Actor string `json:"actor"`
	Round int    `json:"round"`
}

// Session é uma sessão de jogo.
type Session struct {
	Number        int        `json:"number"`
	EnvironmentID string     `json:"environment_id"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Log           []LogEntry `json:"log"`
	// Turn é opcional: sessões gravadas antes da Fase 4 não o têm e
	// recomeçam do primeiro da iniciativa, na rodada 1.
	Turn *TurnState `json:"turn,omitempty"`
}

// Active informa se a sessão está aberta.
func (s *Session) Active() bool { return s.EndedAt == nil }

// Append adiciona uma entrada ao log.
func (s *Session) Append(e LogEntry) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	s.Log = append(s.Log, e)
}

// New cria uma campanha vazia.
func New(id, name string) *Campaign {
	return &Campaign{ID: id, Name: name, CreatedAt: time.Now().UTC()}
}

// AddCharacter vincula um personagem (idempotente).
func (c *Campaign) AddCharacter(id string) {
	for _, existing := range c.CharacterIDs {
		if existing == id {
			return
		}
	}
	c.CharacterIDs = append(c.CharacterIDs, id)
}

// ActiveSession devolve a sessão aberta, se houver.
func (c *Campaign) ActiveSession() (*Session, error) {
	if n := len(c.Sessions); n > 0 && c.Sessions[n-1].Active() {
		return c.Sessions[n-1], nil
	}
	return nil, ErrNoActiveSession
}

// StartSession abre uma nova sessão no ambiente indicado.
func (c *Campaign) StartSession(environmentID string, now time.Time) (*Session, error) {
	if _, err := c.ActiveSession(); err == nil {
		return nil, ErrSessionActive
	}
	if environmentID == "" {
		return nil, fmt.Errorf("campaign: ambiente obrigatório")
	}
	s := &Session{Number: len(c.Sessions) + 1, EnvironmentID: environmentID, StartedAt: now.UTC()}
	c.Sessions = append(c.Sessions, s)
	return s, nil
}

// EndSession encerra a sessão ativa.
func (c *Campaign) EndSession(now time.Time) error {
	s, err := c.ActiveSession()
	if err != nil {
		return err
	}
	t := now.UTC()
	s.EndedAt = &t
	return nil
}
