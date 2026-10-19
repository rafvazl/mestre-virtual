package character

import (
	"errors"
	"math"
	"testing"
)

// Regressão: delta vem do LLM via parser (amount aceita qualquer int).
// Com soma comum, curar math.MaxInt dava overflow, HP virava negativo e o
// clamp levava a 0 — a cura matava o personagem.
func TestApplyHPChangeExtremeDeltas(t *testing.T) {
	cases := []struct {
		name      string
		hp, delta int
		wantHP    int
		wantDelta int
	}{
		{"cura máxima", 4, math.MaxInt, 10, 6},
		{"dano máximo", 4, math.MinInt, 0, -4},
		{"cura em PV cheio", 10, math.MaxInt, 10, 0},
		{"dano em PV zero", 0, math.MinInt, 0, 0},
		{"zero", 7, 0, 7, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTest()
			c.HP = tc.hp
			d := c.ApplyHPChange(tc.delta)
			if c.HP != tc.wantHP || d != tc.wantDelta {
				t.Fatalf("HP %d delta %d, quer HP %d delta %d", c.HP, d, tc.wantHP, tc.wantDelta)
			}
		})
	}
}

// Regressão: AddItem acumulando até estourar int deixava quantidade negativa.
func TestAddItemOverflow(t *testing.T) {
	c := newTest()
	if err := c.AddItem("moeda", math.MaxInt); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem("moeda", 1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("err = %v", err)
	}
	if c.Inventory["moeda"] != math.MaxInt {
		t.Fatalf("quantidade alterada após falha: %d", c.Inventory["moeda"])
	}
}

func TestSatAdd(t *testing.T) {
	cases := [][3]int{
		{1, 2, 3},
		{math.MaxInt, 1, math.MaxInt},
		{math.MinInt, -1, math.MinInt},
		{math.MaxInt, math.MinInt, -1},
		{-5, math.MinInt, math.MinInt},
	}
	for _, c := range cases {
		if got := satAdd(c[0], c[1]); got != c[2] {
			t.Errorf("satAdd(%d, %d) = %d, quer %d", c[0], c[1], got, c[2])
		}
	}
}
