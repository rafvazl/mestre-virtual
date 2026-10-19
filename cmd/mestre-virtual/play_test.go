package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/storage"
)

func TestPlay(t *testing.T) {
	content := filepath.Join("..", "..", "content")
	data := t.TempDir()
	var out, logs bytes.Buffer
	for _, c := range [][]string{{"aria", "Aria", "guerreiro"}, {"bruno", "Bruno", "mago"}} {
		args := []string{"character", "create", "-content", content, "-data", data, "-id", c[0], "-name", c[1], "-class", c[2]}
		if err := run(args, &out); err != nil {
			t.Fatal(err)
		}
	}
	play := func(stdin string, extra ...string) string {
		t.Helper()
		out.Reset()
		logs.Reset()
		args := append([]string{"play", "-content", content, "-data", data, "-campaign", "cripta"}, extra...)
		if err := runIO(args, strings.NewReader(stdin), &out, &logs); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}

	// Campanha nova exige personagens e ambiente.
	if err := runIO([]string{"play", "-content", content, "-data", data, "-campaign", "cripta"}, strings.NewReader(""), &out, &logs); err == nil {
		t.Fatal("criar campanha sem -chars/-env deveria falhar")
	}

	got := play("/status\nabro a porta\n/quem\n/quem aria\nacendo a tocha\n/passar\n/quem\n/dançar\n/sair\n",
		"-chars", "aria,bruno", "-env", "entrada-cripta", "-llm", "local")
	for _, want := range []string{
		"Iniciando a sessão 1", "Entrada da cripta", "/passar (passa a vez)",
		"Aria (aria, guerreiro) PV 12/12", "Espada curta [espada-curta] x1",
		"\naria> O mestre ouve: Aria (aria): abro a porta",
		"Vez de Bruno (bruno), rodada 1. Iniciativa: aria → bruno",
		"a vez segue a ordem de iniciativa e não pode ser trocada",
		"\nbruno> O mestre ouve: Bruno (bruno): acendo a tocha",
		"— Rodada 2 —", "\naria> Aria passa a vez.",
		"Vez de Bruno (bruno), rodada 2.",
		`comando desconhecido "/dançar"`, "Sessão encerrada e salva.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saída sem %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[MECHANICS]") || strings.Contains(got, "[NARRATIVE]") {
		t.Errorf("marcadores vazaram:\n%s", got)
	}
	if logs.Len() != 0 {
		t.Errorf("nível padrão (warn) não deveria logar um jogo sem falhas:\n%s", logs.String())
	}

	st, _ := storage.NewFileStore(data)
	camp, err := st.LoadCampaign("cripta")
	if err != nil {
		t.Fatal(err)
	}
	if len(camp.Sessions) != 1 || camp.Sessions[0].Active() || len(camp.Sessions[0].Log) != 5 {
		t.Fatalf("sessão persistida %+v", camp.Sessions[0])
	}
	if pass := camp.Sessions[0].Log[4]; pass.Kind != "pass" || pass.Actor != "aria" {
		t.Errorf("passagem registrada %+v", pass)
	}

	// Reabrir sem flags abre a sessão 2; com remote, o erro é claro, a vez
	// não muda e o REPL segue.
	got = play("olho em volta\n", "-llm", "remote")
	for _, want := range []string{"Iniciando a sessão 2", "o mestre não conseguiu narrar", "Nada foi aplicado; a vez continua com aria", "sessão pausada"} {
		if !strings.Contains(got, want) {
			t.Errorf("saída sem %q:\n%s", want, got)
		}
	}
	if !strings.Contains(logs.String(), "level=ERROR msg=llm.error provider=remote") {
		t.Errorf("falha do provider deveria ir para o log (stderr):\n%s", logs.String())
	}
	if strings.Contains(out.String(), "llm.error") {
		t.Error("log não pode sair no stdout")
	}

	// EOF pausou a sessão 2: a próxima execução a retoma, e a vez também.
	got = play("olho em volta\n")
	if !strings.Contains(got, "Retomando a sessão 2") || !strings.Contains(got, "\naria> O mestre ouve") {
		t.Errorf("esperava retomar a sessão 2 com aria:\n%s", got)
	}
	got = play("/quem\n")
	if !strings.Contains(got, "Retomando a sessão 2") || !strings.Contains(got, "Vez de Bruno (bruno), rodada 1") {
		t.Errorf("a vez de bruno deveria ser retomada:\n%s", got)
	}

	// Bruno com PV 0 é pulado (e continua no grupo).
	bruno, _ := st.LoadCharacter("bruno")
	bruno.HP = 0
	if err := st.SaveCharacter(bruno); err != nil {
		t.Fatal(err)
	}
	got = play("/quem\nespero\n/sair\n")
	for _, want := range []string{"Vez de Aria (aria), rodada 2. Iniciativa: aria → bruno (PV 0, pulado)", "\naria> O mestre ouve: Aria (aria): espero"} {
		if !strings.Contains(got, want) {
			t.Errorf("saída sem %q:\n%s", want, got)
		}
	}
	camp, _ = st.LoadCampaign("cripta")
	if s := camp.Sessions[1]; len(s.Log) != 4 || s.Active() {
		t.Errorf("sessão 2: %+v", s)
	}
}
