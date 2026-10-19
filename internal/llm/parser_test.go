package llm

import "testing"

func TestParseResponse(t *testing.T) {
	raw := "[NARRATIVE]\nO esqueleto golpeia.\n\n[MECHANICS]\nHP_CHANGE target=aria amount=-3\nITEM_GAINED target=aria item=moeda-ouro qty=5\nITEM_LOST target=aria item=tocha\n# comentário\nFOO target=x\nHP_CHANGE amount=2\nITEM_GAINED target=aria item=pocao-cura qty=0\n"
	got, errs := ParseResponse(raw)
	if got.Narrative != "O esqueleto golpeia." {
		t.Fatalf("narrativa = %q", got.Narrative)
	}
	want := []StructuredEffect{
		{Type: EffectHPChange, Target: "aria", Amount: -3},
		{Type: EffectItemGained, Target: "aria", ItemID: "moeda-ouro", Amount: 5},
		{Type: EffectItemLost, Target: "aria", ItemID: "tocha", Amount: 1},
	}
	if len(got.Effects) != len(want) {
		t.Fatalf("efeitos = %+v", got.Effects)
	}
	for i := range want {
		if got.Effects[i] != want[i] {
			t.Errorf("efeito %d = %+v, quer %+v", i, got.Effects[i], want[i])
		}
	}
	if len(errs) != 3 {
		t.Fatalf("esperava 3 erros, veio %d: %v", len(errs), errs)
	}
}

func TestParseResponseSemMarcadores(t *testing.T) {
	got, errs := ParseResponse("só narração livre")
	if got.Narrative != "só narração livre" || len(got.Effects) != 0 || len(errs) != 0 {
		t.Fatalf("got %+v errs %v", got, errs)
	}
}
