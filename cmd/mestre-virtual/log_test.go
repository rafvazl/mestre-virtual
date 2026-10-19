package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFlags(t *testing.T) {
	var out, logs bytes.Buffer
	ask := func(extra ...string) error {
		out.Reset()
		logs.Reset()
		args := append([]string{"ask", "-llm", "local"}, extra...)
		return runIO(append(args, "abro a porta"), strings.NewReader(""), &out, &logs)
	}

	// Padrão: warn em texto; um ask bem-sucedido não loga nada.
	if err := ask(); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("log inesperado: %s", logs.String())
	}

	// info + json: uma linha JSON por chamada ao provider, só no stderr.
	if err := ask("-log-level", "info", "-log-format", "json"); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &rec); err != nil {
		t.Fatalf("log não é JSON: %v\n%s", err, logs.String())
	}
	if rec["msg"] != "llm.done" || rec["provider"] != "local/echo" || rec["op"] != "generate" {
		t.Errorf("registro %v", rec)
	}
	if strings.Contains(out.String(), "llm.done") || !strings.Contains(out.String(), "abro a porta") {
		t.Errorf("stdout: %s", out.String())
	}

	for _, bad := range [][]string{{"-log-level", "verbose"}, {"-log-format", "xml"}} {
		if err := ask(bad...); err == nil {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestPlayMemory(t *testing.T) {
	content := filepath.Join("..", "..", "content")
	data := t.TempDir()
	var out, logs bytes.Buffer
	if err := run([]string{"character", "create", "-content", content, "-data", data, "-id", "aria", "-name", "Aria", "-class", "guerreiro"}, &out); err != nil {
		t.Fatal(err)
	}
	play := func(campaign, stdin string, extra ...string) {
		t.Helper()
		args := append([]string{"play", "-content", content, "-data", data, "-campaign", campaign,
			"-chars", "aria", "-env", "entrada-cripta", "-log-level", "debug"}, extra...)
		if err := runIO(args, strings.NewReader(stdin), &out, &logs); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
	}

	play("cripta", "falo com o velho guardião\n/sair\n")
	raw, err := os.ReadFile(filepath.Join(data, "memory", "cripta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Entries  []struct{ Kind, Text string }
		Archived []struct{ Text string }
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	// /sair publica SessionEnded: as 2 entradas do turno viram 1 resumo.
	if len(file.Entries) != 1 || file.Entries[0].Kind != "summary" || len(file.Archived) != 2 ||
		!strings.Contains(file.Entries[0].Text, "velho guardião") {
		t.Fatalf("memória em disco:\n%s", raw)
	}

	// Na sessão 2 o resumo entra no prompt (registrado em debug pelo Engine).
	logs.Reset()
	play("cripta", "pergunto pelo guardião\n")
	sc := bufio.NewScanner(&logs)
	found := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "memória no prompt") && strings.Contains(sc.Text(), "entries=1") {
			found = true
		}
	}
	if !found {
		t.Errorf("memória não entrou no prompt:\n%s", logs.String())
	}

	// -memory-budget 0 desliga a memória: nada é gravado.
	play("sem-memoria", "olho em volta\n/sair\n", "-memory-budget", "0")
	if _, err := os.Stat(filepath.Join(data, "memory", "sem-memoria.json")); !os.IsNotExist(err) {
		t.Errorf("memória deveria estar desligada: %v", err)
	}
}
