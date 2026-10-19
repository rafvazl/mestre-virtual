package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeContent cria root/<sub>/<name> com data.
func writeContent(t *testing.T, root, sub, name, data string) {
	t.Helper()
	dir := filepath.Join(root, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreMissingDirs(t *testing.T) {
	// Raiz inexistente: aceita e devolve base vazia (comportamento documentado).
	s, err := NewFileStore(filepath.Join(t.TempDir(), "nao-existe"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range []RefType{RefItem, RefClass, RefEnvironment, RefNPC} {
		if ids := s.List(rt); len(ids) != 0 {
			t.Errorf("%s: %v", rt, ids)
		}
	}
	// Só alguns subdiretórios presentes.
	root := t.TempDir()
	writeContent(t, root, "items", "a.json", `{"id":"tocha"}`)
	s, err = NewFileStore(root)
	if err != nil || !s.Exists(Ref{Type: RefItem, ID: "tocha"}) {
		t.Fatalf("s %v err %v", s, err)
	}
}

func TestFileStoreRootIsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "arquivo")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := NewFileStore(f); err == nil {
		t.Fatal("raiz que é arquivo deveria falhar")
	}
}

func TestFileStoreObjectVsArray(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "items", "um.json", `{"id":"tocha","name":"Tocha"}`)
	writeContent(t, root, "items", "varios.json", "\n\t [ {\"id\":\"corda\"}, {\"id\":\"pao\"} ]")
	writeContent(t, root, "items", "vazio.json", `[]`)
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(RefItem); !reflect.DeepEqual(got, []string{"corda", "pao", "tocha"}) {
		t.Fatalf("itens = %v", got)
	}
	if it, _ := s.Item("tocha"); it.Name != "Tocha" {
		t.Fatalf("item = %+v", it)
	}
}

func TestFileStoreIgnoresNonJSONAndSubdirs(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "items", "leia-me.txt", "não é json")
	writeContent(t, root, "items", "a.json.bak", "{")
	writeContent(t, root, filepath.Join("items", "sub.json"), "b.json", `{"id":"escondido"}`)
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if ids := s.List(RefItem); len(ids) != 0 {
		t.Fatalf("itens = %v", ids)
	}
}

func TestFileStoreInvalidJSON(t *testing.T) {
	cases := map[string]string{
		"truncado":         `{"id":"x"`,
		"vazio":            ``,
		"tipo errado":      `{"id":1}`,
		"array malformado": `[{"id":"a"},]`,
		"lixo":             `olá`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeContent(t, root, "classes", "ruim.json", data)
			_, err := NewFileStore(root)
			if err == nil {
				t.Fatal("esperava erro")
			}
			if !strings.Contains(err.Error(), "ruim.json") {
				t.Fatalf("erro deveria citar o arquivo: %v", err)
			}
		})
	}
}

func TestFileStoreMissingID(t *testing.T) {
	for name, data := range map[string]string{
		"objeto sem id":  `{"name":"Sem ID"}`,
		"null":           `null`,
		"array com null": `[{"id":"a"}, null]`,
		"id vazio":       `[{"id":""}]`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeContent(t, root, "npcs", "n.json", data)
			if _, err := NewFileStore(root); err == nil || !strings.Contains(err.Error(), "sem id") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFileStoreDuplicateAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "items", "a.json", `{"id":"x"}`)
	writeContent(t, root, "items", "b.json", `{"id":"x"}`)
	if _, err := NewFileStore(root); err == nil || !strings.Contains(err.Error(), "duplicado") {
		t.Fatalf("err = %v", err)
	}
	// Mesmo id em tipos diferentes é permitido (namespaces separados).
	root = t.TempDir()
	writeContent(t, root, "items", "a.json", `{"id":"x"}`)
	writeContent(t, root, "npcs", "a.json", `{"id":"x"}`)
	if _, err := NewFileStore(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidationReportsAllDanglingRefs(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "npcs", "n.json", `{"id":"goblin","loot":["ouro"]}`)
	writeContent(t, root, "environments", "e.json", `{"id":"sala","exits":["corredor"]}`)
	_, err := NewFileStore(root)
	if err == nil {
		t.Fatal("esperava erro")
	}
	for _, want := range []string{`"goblin"`, `"ouro"`, `"sala"`, `"corredor"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erro sem %s: %v", want, err)
		}
	}
}

func TestExistsAndListUnknownType(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	if s.Exists(Ref{Type: "arma", ID: "x"}) || s.List("arma") != nil {
		t.Fatal("tipo desconhecido não deveria existir")
	}
}
