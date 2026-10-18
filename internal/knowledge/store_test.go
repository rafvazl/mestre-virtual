package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoContentLoads(t *testing.T) {
	s, err := NewFileStore(filepath.Join("..", "..", "content"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Class("guerreiro")
	if err != nil || c.BaseHP <= 0 {
		t.Fatalf("classe %+v err %v", c, err)
	}
	if !s.Exists(Ref{Type: RefItem, ID: "espada-curta"}) {
		t.Error("espada-curta deveria existir")
	}
	if _, err := s.NPC("dragao"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if len(s.List(RefEnvironment)) == 0 {
		t.Error("esperava ambientes")
	}
}

func TestParseRef(t *testing.T) {
	r, err := ParseRef("item:tocha")
	if err != nil || r != (Ref{Type: RefItem, ID: "tocha"}) || r.String() != "item:tocha" {
		t.Fatalf("r %+v err %v", r, err)
	}
	for _, bad := range []string{"tocha", "arma:x", "item:"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("%q deveria falhar", bad)
		}
	}
}

func TestValidationCatchesDanglingRefs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "classes"), 0o755)
	os.WriteFile(filepath.Join(dir, "classes", "c.json"), []byte(`{"id":"x","starting_items":["nada"]}`), 0o644)
	if _, err := NewFileStore(dir); err == nil {
		t.Fatal("esperava erro de integridade referencial")
	}
}

func TestDuplicateID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "items"), 0o755)
	os.WriteFile(filepath.Join(dir, "items", "a.json"), []byte(`[{"id":"a"},{"id":"a"}]`), 0o644)
	if _, err := NewFileStore(dir); err == nil {
		t.Fatal("esperava erro de id duplicado")
	}
}
