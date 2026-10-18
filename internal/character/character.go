// Package character modela o estado mutável de um personagem: PV e
// inventário. Toda mutação passa por métodos que validam invariantes
// (PV entre 0 e MaxHP, quantidades positivas).
package character

import (
	"errors"
	"fmt"
	"sort"

	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

// Erros de domínio.
var (
	ErrInvalidAmount = errors.New("character: quantidade inválida")
	ErrNotEnough     = errors.New("character: itens insuficientes no inventário")
)

// Character é um personagem jogador (ou NPC instanciado).
type Character struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Player    string         `json:"player,omitempty"`
	ClassID   string         `json:"class_id"`
	HP        int            `json:"hp"`
	MaxHP     int            `json:"max_hp"`
	Inventory map[string]int `json:"inventory"` // itemID -> quantidade
}

// New cria um personagem a partir de uma classe da base de conhecimento.
func New(id, name string, class knowledge.Class) *Character {
	c := &Character{
		ID:        id,
		Name:      name,
		ClassID:   class.ID,
		HP:        class.BaseHP,
		MaxHP:     class.BaseHP,
		Inventory: map[string]int{},
	}
	for _, it := range class.StartingItems {
		c.Inventory[it]++
	}
	return c
}

// Alive informa se o personagem está de pé.
func (c *Character) Alive() bool { return c.HP > 0 }

// ApplyHPChange aplica um delta de PV com clamp em [0, MaxHP] e devolve o
// delta efetivamente aplicado.
func (c *Character) ApplyHPChange(delta int) int {
	before := c.HP
	c.HP += delta
	if c.HP < 0 {
		c.HP = 0
	}
	if c.HP > c.MaxHP {
		c.HP = c.MaxHP
	}
	return c.HP - before
}

// AddItem adiciona qty unidades de itemID.
func (c *Character) AddItem(itemID string, qty int) error {
	if qty <= 0 || itemID == "" {
		return ErrInvalidAmount
	}
	if c.Inventory == nil {
		c.Inventory = map[string]int{}
	}
	c.Inventory[itemID] += qty
	return nil
}

// RemoveItem remove qty unidades de itemID; falha sem alterar nada se não houver o bastante.
func (c *Character) RemoveItem(itemID string, qty int) error {
	if qty <= 0 || itemID == "" {
		return ErrInvalidAmount
	}
	have := c.Inventory[itemID]
	if have < qty {
		return fmt.Errorf("%w: %s (tem %d, precisa %d)", ErrNotEnough, itemID, have, qty)
	}
	if have == qty {
		delete(c.Inventory, itemID)
	} else {
		c.Inventory[itemID] = have - qty
	}
	return nil
}

// Items devolve os IDs do inventário ordenados (saída determinística).
func (c *Character) Items() []string {
	ids := make([]string, 0, len(c.Inventory))
	for id := range c.Inventory {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
