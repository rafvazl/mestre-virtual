package character

import (
	"errors"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

func newTest() *Character {
	return New("aria", "Aria", knowledge.Class{ID: "guerreiro", BaseHP: 10, StartingItems: []string{"espada-curta", "pocao-cura", "pocao-cura"}})
}

func TestNewFromClass(t *testing.T) {
	c := newTest()
	if c.HP != 10 || c.MaxHP != 10 || c.Inventory["pocao-cura"] != 2 {
		t.Fatalf("%+v", c)
	}
}

func TestHPClamp(t *testing.T) {
	c := newTest()
	if d := c.ApplyHPChange(-15); d != -10 || c.HP != 0 || c.Alive() {
		t.Fatalf("d %d hp %d", d, c.HP)
	}
	if d := c.ApplyHPChange(50); d != 10 || c.HP != 10 {
		t.Fatalf("d %d hp %d", d, c.HP)
	}
}

func TestInventory(t *testing.T) {
	c := newTest()
	if err := c.RemoveItem("pocao-cura", 3); !errors.Is(err, ErrNotEnough) {
		t.Fatalf("err %v", err)
	}
	if c.Inventory["pocao-cura"] != 2 {
		t.Fatal("falha não deveria alterar o inventário")
	}
	if err := c.RemoveItem("pocao-cura", 2); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Inventory["pocao-cura"]; ok {
		t.Fatal("item zerado deveria sair do mapa")
	}
	if err := c.AddItem("tocha", 0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("err %v", err)
	}
	c.AddItem("tocha", 1)
	if got := c.Items(); len(got) != 2 || got[0] != "espada-curta" || got[1] != "tocha" {
		t.Fatalf("items %v", got)
	}
}
