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
	var out bytes.Buffer
	for _, c := range [][]string{{"aria", "Aria", "guerreiro"}, {"bruno", "Bruno", "mago"}} {
		args := []string{"character", "create", "-content", content, "-data", data, "-id", c[0], "-name", c[1], "-class", c[2]}
		if err := run(args, &out); err != nil {
			t.Fatal(err)
		}
	}
	play := func(stdin string, extra ...string) string {
		t.Helper()
		out.Reset()
		args := append([]string{"play", "-content", content, "-data", data, "-campaign", "cripta"}, extra...)
		if err := runIO(args, strings.NewReader(stdin), &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}

	// Campanha nova exige personagens e ambiente.
	if err := runIO([]string{"play", "-content", content, "-data", data, "-campaign", "cripta"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("criar campanha sem -chars/-env deveria falhar")
	}

	got := play("/status\nabro a porta\n/quem bruno\n/quem zed\nacendo a tocha\n/dançar\n/sair\n",
		"-chars", "aria,bruno", "-env", "entrada-cripta", "-llm", "local")
	for _, want := range []string{
		"Iniciando a sessão 1", "Entrada da cripta",
		"Aria (aria, guerreiro) PV 12/12", "Espada curta [espada-curta] x1",
		"O mestre ouve: Aria (aria): abro a porta",
		"Agora jogando com Bruno.", `personagem "zed" não está na campanha`,
		"O mestre ouve: Bruno (bruno): acendo a tocha",
		`comando desconhecido "/dançar"`, "Sessão encerrada e salva.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saída sem %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[MECHANICS]") || strings.Contains(got, "[NARRATIVE]") {
		t.Errorf("marcadores vazaram:\n%s", got)
	}

	st, _ := storage.NewFileStore(data)
	camp, err := st.LoadCampaign("cripta")
	if err != nil {
		t.Fatal(err)
	}
	if len(camp.Sessions) != 1 || camp.Sessions[0].Active() || len(camp.Sessions[0].Log) != 4 {
		t.Fatalf("sessão persistida %+v", camp.Sessions[0])
	}

	// Reabrir sem flags abre a sessão 2; com remote, o erro é claro e o REPL segue.
	got = play("olho em volta\n", "-llm", "remote")
	for _, want := range []string{"Iniciando a sessão 2", "o mestre não conseguiu narrar", "Nada foi aplicado", "sessão pausada"} {
		if !strings.Contains(got, want) {
			t.Errorf("saída sem %q:\n%s", want, got)
		}
	}

	// EOF pausou a sessão 2: a próxima execução a retoma.
	got = play("/sair\n")
	if !strings.Contains(got, "Retomando a sessão 2") {
		t.Errorf("esperava retomar a sessão 2:\n%s", got)
	}
	camp, _ = st.LoadCampaign("cripta")
	if len(camp.Sessions) != 2 || len(camp.Sessions[1].Log) != 0 {
		t.Errorf("falha do provider não deveria registrar nada: %+v", camp.Sessions[1])
	}
}
