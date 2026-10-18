package llm

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestParseResponseTable(t *testing.T) {
	hp := func(target string, n int) StructuredEffect {
		return StructuredEffect{Type: EffectHPChange, Target: target, Amount: n}
	}
	cases := []struct {
		name      string
		raw       string
		narrative string
		effects   []StructuredEffect
		nErrs     int
	}{
		{name: "vazio", raw: ""},
		{name: "só marcadores", raw: "[NARRATIVE]\n[MECHANICS]\n"},
		{name: "marcadores minúsculos e com espaço", raw: "  [narrative] \nolá\n\t[Mechanics]\nhp_change target=a amount=1", narrative: "olá", effects: []StructuredEffect{hp("a", 1)}},
		{name: "CRLF", raw: "[NARRATIVE]\r\nlinha 1\r\nlinha 2\r\n[MECHANICS]\r\nHP_CHANGE target=a amount=-2\r\n", narrative: "linha 1\nlinha 2", effects: []StructuredEffect{hp("a", -2)}},
		{name: "chaves sem diferenciar caixa", raw: "[MECHANICS]\nHP_CHANGE TARGET=a AMOUNT=+3", effects: []StructuredEffect{hp("a", 3)}},
		{name: "chave duplicada: última vence", raw: "[MECHANICS]\nHP_CHANGE target=a target=b amount=1", effects: []StructuredEffect{hp("b", 1)}},
		{name: "chave extra é aceita", raw: "[MECHANICS]\nHP_CHANGE target=a amount=1 motivo=queda", effects: []StructuredEffect{hp("a", 1)}},
		{name: "valor com '=' interno", raw: "[MECHANICS]\nITEM_GAINED target=a item=x=y", effects: []StructuredEffect{{Type: EffectItemGained, Target: "a", ItemID: "x=y", Amount: 1}}},
		{name: "amount ausente", raw: "[MECHANICS]\nHP_CHANGE target=a", nErrs: 1},
		{name: "amount não numérico", raw: "[MECHANICS]\nHP_CHANGE target=a amount=muito", nErrs: 1},
		{name: "amount fora de int64", raw: "[MECHANICS]\nHP_CHANGE target=a amount=99999999999999999999", nErrs: 1},
		{name: "argumento sem '='", raw: "[MECHANICS]\nHP_CHANGE target=a amount=1 extra", nErrs: 1},
		{name: "chave vazia", raw: "[MECHANICS]\nHP_CHANGE =a target=a amount=1", nErrs: 1},
		{name: "valor vazio", raw: "[MECHANICS]\nHP_CHANGE target= amount=1", nErrs: 1},
		{name: "item ausente", raw: "[MECHANICS]\nITEM_LOST target=a", nErrs: 1},
		{name: "qty negativo", raw: "[MECHANICS]\nITEM_LOST target=a item=x qty=-1", nErrs: 1},
		{name: "tipo desconhecido", raw: "[MECHANICS]\nTELEPORT target=a", nErrs: 1},
		{name: "comentários e linhas vazias", raw: "[MECHANICS]\n\n# nada\n   # indentado\n", nErrs: 0},
		{name: "volta para narrativa", raw: "[NARRATIVE]\nA\n[MECHANICS]\nHP_CHANGE target=a amount=1\n[NARRATIVE]\nB", narrative: "A\nB", effects: []StructuredEffect{hp("a", 1)}},
		{name: "marcador no meio da linha não conta", raw: "texto [MECHANICS]\nHP_CHANGE target=a amount=1", narrative: "texto [MECHANICS]\nHP_CHANGE target=a amount=1"},
		{name: "narrativa preserva indentação interna", raw: "[NARRATIVE]\n  a\n    b\n", narrative: "a\n    b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, errs := ParseResponse(tc.raw)
			if got.Narrative != tc.narrative {
				t.Errorf("narrativa = %q, quer %q", got.Narrative, tc.narrative)
			}
			if !reflect.DeepEqual(got.Effects, tc.effects) {
				t.Errorf("efeitos = %+v, quer %+v", got.Effects, tc.effects)
			}
			if len(errs) != tc.nErrs {
				t.Errorf("erros = %v, quer %d", errs, tc.nErrs)
			}
		})
	}
}

func TestParseErrorCarriesLine(t *testing.T) {
	_, errs := ParseResponse("[NARRATIVE]\nx\n[MECHANICS]\n\nFOO target=a")
	var pe *ParseError
	if len(errs) != 1 || !errors.As(errs[0], &pe) || pe.Line != 5 || pe.Text != "FOO target=a" {
		t.Fatalf("errs %v", errs)
	}
	if !strings.Contains(pe.Error(), "linha 5") {
		t.Fatalf("mensagem: %s", pe.Error())
	}
}

// Regressão: com bufio.Scanner, uma linha > 64 KiB fazia o parser parar em
// silêncio e devolver narrativa vazia, nenhum efeito e nenhum erro.
func TestParseResponseLongLine(t *testing.T) {
	long := strings.Repeat("a", 200*1024)
	got, errs := ParseResponse("[NARRATIVE]\n" + long + "\n[MECHANICS]\nHP_CHANGE target=aria amount=-3\n")
	if len(got.Narrative) != len(long) || len(errs) != 0 {
		t.Fatalf("narrativa %d bytes, errs %v", len(got.Narrative), errs)
	}
	if len(got.Effects) != 1 || got.Effects[0].Amount != -3 {
		t.Fatalf("efeitos %+v", got.Effects)
	}
}

func FuzzParseResponse(f *testing.F) {
	for _, s := range []string{
		"",
		"só narração",
		"[NARRATIVE]\nO esqueleto golpeia.\n[MECHANICS]\nHP_CHANGE target=aria amount=-3\nITEM_GAINED target=aria item=moeda qty=5\nITEM_LOST target=aria item=tocha\n",
		"[MECHANICS]\nHP_CHANGE target= amount=1\nITEM_GAINED target=a item=x qty=0\n=\n",
		"[mechanics]\r\nitem_lost TARGET=a ITEM=b QTY=+2\r\n# c\r\n",
		"[MECHANICS]\nHP_CHANGE target=a amount=9223372036854775807\n",
		"[MECHANICS]\nITEM_GAINED target=  item=x\n",
		"\xff\xfe[MECHANICS]\n\x00 target=a\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, errs := ParseResponse(raw)
		again, errs2 := ParseResponse(raw)
		if !reflect.DeepEqual(got, again) || len(errs) != len(errs2) {
			t.Fatal("parser não determinístico")
		}
		if got.Narrative != strings.TrimSpace(got.Narrative) {
			t.Fatalf("narrativa não aparada: %q", got.Narrative)
		}
		for _, e := range got.Effects {
			if e.Target == "" || strings.IndexFunc(e.Target, unicode.IsSpace) >= 0 {
				t.Fatalf("target inválido: %+v", e)
			}
			switch e.Type {
			case EffectHPChange:
				if e.ItemID != "" {
					t.Fatalf("HP_CHANGE com item: %+v", e)
				}
			case EffectItemGained, EffectItemLost:
				if e.ItemID == "" || e.Amount <= 0 {
					t.Fatalf("efeito de item inválido: %+v", e)
				}
			default:
				t.Fatalf("tipo inesperado: %+v", e)
			}
		}
		for _, err := range errs {
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Line < 1 || pe.Text == "" {
				t.Fatalf("erro sem linha/texto: %#v", err)
			}
		}
	})
}
