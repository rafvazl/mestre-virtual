// Package knowledge é a base de conhecimento estática do jogo: itens,
// classes, ambientes e modelos de NPC. É conteúdo "de livro", versionado em
// content/, e nunca é alterado durante uma sessão — estado mutável fica em
// campaign/character/storage.
package knowledge

import (
	"fmt"
	"strings"
)

// RefType é o tipo de entidade referenciada.
type RefType string

const (
	RefItem        RefType = "item"
	RefClass       RefType = "class"
	RefEnvironment RefType = "environment"
	RefNPC         RefType = "npc"
)

// Valid informa se o tipo é conhecido.
func (t RefType) Valid() bool {
	switch t {
	case RefItem, RefClass, RefEnvironment, RefNPC:
		return true
	}
	return false
}

// Ref é uma referência tipada a uma entidade da base ("item:espada-curta").
// Tipar a referência evita que um ID de item seja usado onde se espera NPC.
type Ref struct {
	Type RefType `json:"type"`
	ID   string  `json:"id"`
}

// String implementa fmt.Stringer no formato "tipo:id".
func (r Ref) String() string { return string(r.Type) + ":" + r.ID }

// ParseRef converte "tipo:id" em Ref.
func ParseRef(s string) (Ref, error) {
	t, id, ok := strings.Cut(s, ":")
	r := Ref{Type: RefType(t), ID: id}
	if !ok || id == "" || !r.Type.Valid() {
		return Ref{}, fmt.Errorf("knowledge: ref inválida %q", s)
	}
	return r, nil
}

// Item é um objeto do mundo.
type Item struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Weight      float64  `json:"weight"`
	Value       int      `json:"value"`
	Tags        []string `json:"tags,omitempty"`
	// Damage em notação de dados (ex.: "1d6"), vazio se não for arma.
	Damage string `json:"damage,omitempty"`
}

// Class é uma classe de personagem.
type Class struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	HitDie        int      `json:"hit_die"`
	BaseHP        int      `json:"base_hp"`
	StartingItems []string `json:"starting_items,omitempty"`
}

// Environment é um local/cenário.
type Environment struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Hazards     []string `json:"hazards,omitempty"`
	Exits       []string `json:"exits,omitempty"`
}

// NPCTemplate é o molde de um NPC (instâncias vivem no estado da sessão).
type NPCTemplate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Personality string   `json:"personality"`
	HP          int      `json:"hp"`
	Hostile     bool     `json:"hostile"`
	Loot        []string `json:"loot,omitempty"`
}
