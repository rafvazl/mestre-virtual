package storage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

func TestInvalidIDs(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	bad := []string{
		"", ".", "..", "../x", "a/b", `a\b`, ".oculto", "-x", "_x", "a b", "ação", "a.json",
		strings.Repeat("a", 65), "x\x00",
	}
	for _, id := range bad {
		if err := s.SaveCharacter(&character.Character{ID: id}); err == nil {
			t.Errorf("Save %q deveria falhar", id)
		}
		if err := s.SaveCampaign(campaign.New(id, "x")); err == nil {
			t.Errorf("SaveCampaign %q deveria falhar", id)
		}
		if _, err := s.LoadCharacter(id); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Load %q: err = %v (esperava id inválido, não NotFound)", id, err)
		}
	}
	// Nada pode ter sido escrito.
	for _, d := range []string{"campaigns", "characters"} {
		if es, _ := os.ReadDir(filepath.Join(root, d)); len(es) != 0 {
			t.Fatalf("%s não está vazio: %v", d, es)
		}
	}
	// Limites válidos.
	for _, id := range []string{"a", "A1", "x_y-z", strings.Repeat("a", 64)} {
		if err := s.SaveCharacter(&character.Character{ID: id}); err != nil {
			t.Errorf("Save %q: %v", id, err)
		}
	}
}

func TestListIgnoresTempAndForeignFiles(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	s.SaveCharacter(character.New("aria", "Aria", knowledge.Class{ID: "c", BaseHP: 5}))
	dir := filepath.Join(root, "characters")
	for _, name := range []string{".tmp-123", ".tmp-456.json", "notas.txt", "notas antigas.json", "x.json.bak"} {
		os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "pasta.json"), 0o755)

	ids, err := s.ListCharacters()
	if err != nil || !reflect.DeepEqual(ids, []string{"aria"}) {
		t.Fatalf("ids %v err %v", ids, err)
	}
	// Regressão: tudo que List devolve precisa ser carregável. Antes,
	// "notas antigas.json" aparecia na lista e Load falhava por id inválido.
	for _, id := range ids {
		if _, err := s.LoadCharacter(id); err != nil {
			t.Fatalf("List devolveu %q, mas Load falhou: %v", id, err)
		}
	}
}

func TestListSorted(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	for _, id := range []string{"c", "a", "b"} {
		s.SaveCampaign(campaign.New(id, id))
	}
	if ids, _ := s.ListCampaigns(); !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatal(ids)
	}
}

func TestSaveOverwritesAndLeavesNoTemp(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	c := character.New("aria", "Aria", knowledge.Class{ID: "c", BaseHP: 10})
	s.SaveCharacter(c)
	c.HP = 3
	if err := s.SaveCharacter(c); err != nil {
		t.Fatal(err)
	}
	got, _ := s.LoadCharacter("aria")
	if got.HP != 3 {
		t.Fatalf("HP = %d", got.HP)
	}
	es, _ := os.ReadDir(filepath.Join(root, "characters"))
	if len(es) != 1 || es[0].Name() != "aria.json" {
		t.Fatalf("arquivos: %v", es)
	}
}

func TestLoadCorruptedIsNotNotFound(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	os.WriteFile(filepath.Join(root, "characters", "aria.json"), []byte(`{"id":"aria",`), 0o644)
	_, err := s.LoadCharacter("aria")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadCharacterNilInventory(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	os.WriteFile(filepath.Join(root, "characters", "aria.json"), []byte(`{"id":"aria","inventory":null}`), 0o644)
	c, err := s.LoadCharacter("aria")
	if err != nil || c.Inventory == nil {
		t.Fatalf("c %+v err %v", c, err)
	}
	if err := c.AddItem("tocha", 1); err != nil {
		t.Fatal(err)
	}
}

func TestNewFileStoreFailsOnFileRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "arquivo")
	os.WriteFile(f, nil, 0o644)
	if _, err := NewFileStore(f); err == nil {
		t.Fatal("raiz que é arquivo deveria falhar")
	}
}

func TestListMissingDir(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	os.RemoveAll(filepath.Join(root, "campaigns"))
	if _, err := s.ListCampaigns(); err == nil {
		t.Fatal("diretório removido deveria gerar erro")
	}
}
