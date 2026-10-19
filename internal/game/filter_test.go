package game

import (
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/llm"
)

// O filtro deve mostrar exatamente a narrativa que ParseResponse extrai,
// qualquer que seja a fragmentação do stream.
func TestNarrationFilterMatchesParser(t *testing.T) {
	cases := []string{
		"[NARRATIVE]\nA porta range.\n[MECHANICS]\nHP_CHANGE target=aria amount=-2\n",
		"[narrative]\n\nLinha 1.\n\nLinha 2.\n\n[mechanics]\nITEM_GAINED target=aria item=tocha\n",
		"Sem marcadores, tudo é narrativa.\nOutra linha.",
		"[NARRATIVE]\n[sussurro] alguém fala no escuro.\n[MECHANICS]  \nHP_CHANGE target=aria amount=1",
		"[NARRATIVE]\nO texto cita [MECHANICS] no meio da linha.\n[MECHANICS]\n",
		"  [NARRATIVE]  \n  recuado  \n  [MECHANICS]\nHP_CHANGE target=a amount=1\n[NARRATIVE]\nvolta à narrativa\n",
		"[NARRATIVE]\nfinal sem quebra",
		"[NARRATIVE]\n[MECHANICS]\nHP_CHANGE target=a amount=1\n",
	}
	for _, raw := range cases {
		want, _ := llm.ParseResponse(raw)
		for size := 1; size <= len(raw); size++ {
			var out strings.Builder
			f := newNarrationFilter(&out)
			for i := 0; i < len(raw); i += size {
				f.Write(raw[i:min(i+size, len(raw))])
			}
			f.Flush()
			got := out.String()
			if strings.TrimSpace(got) != want.Narrative {
				t.Fatalf("raw %q chunk %d:\n got %q\nwant %q", raw, size, got, want.Narrative)
			}
			if got != "" && !strings.HasSuffix(got, "\n") {
				t.Fatalf("raw %q chunk %d: saída sem quebra final %q", raw, size, got)
			}
			if strings.Contains(strings.ToUpper(got), "HP_CHANGE") || strings.Contains(strings.ToUpper(got), "ITEM_GAINED") {
				t.Fatalf("mecânica vazou: %q", got)
			}
		}
	}
}

// A narrativa deve sair antes do fim do stream (não só no Flush).
func TestNarrationFilterStreamsIncrementally(t *testing.T) {
	var out strings.Builder
	f := newNarrationFilter(&out)
	f.Write("[NARRATIVE]\nA tocha ")
	if out.String() != "A tocha " {
		t.Fatalf("esperava texto parcial imediato, veio %q", out.String())
	}
	f.Write("se apaga.\n[MEC")
	if out.String() != "A tocha se apaga.\n" {
		t.Fatalf("marcador parcial deveria ficar retido, veio %q", out.String())
	}
	f.Write("HANICS]\nHP_CHANGE target=aria amount=-1\n")
	f.Flush()
	if out.String() != "A tocha se apaga.\n" {
		t.Fatalf("saída final %q", out.String())
	}
}
