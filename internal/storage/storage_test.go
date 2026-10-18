package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

func TestRoundTrip(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ch := character.New("aria", "Aria", knowledge.Class{ID: "guerreiro", BaseHP: 10, StartingItems: []string{"tocha"}})
	if err := s.SaveCharacter(ch); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCharacter("aria")
	if err != nil || got.Name != "Aria" || got.Inventory["tocha"] != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}

	c := campaign.New("cripta", "A Cripta")
	c.AddCharacter("aria")
	c.StartSession("entrada-cripta", time.Now())
	if err := s.SaveCampaign(c); err != nil {
		t.Fatal(err)
	}
	gc, err := s.LoadCampaign("cripta")
	if err != nil || len(gc.Sessions) != 1 || !gc.Sessions[0].Active() {
		t.Fatalf("gc %+v err %v", gc, err)
	}
	ids, _ := s.ListCharacters()
	if len(ids) != 1 || ids[0] != "aria" {
		t.Fatal(ids)
	}
}

func TestNotFoundAndBadID(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	if _, err := s.LoadCharacter("nada"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.LoadCharacter("../etc/passwd"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal("id com path traversal deveria ser rejeitado")
	}
}
