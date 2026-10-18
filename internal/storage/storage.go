// Package storage persiste o estado de execução (campanhas e personagens)
// como JSON em disco, sob data/.
//
//	data/campaigns/<id>.json
//	data/characters/<id>.json
//
// A escrita é atômica (arquivo temporário + rename) para não corromper o
// estado se o processo cair no meio de um save.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
)

// ErrNotFound indica que o registro não existe.
var ErrNotFound = errors.New("storage: não encontrado")

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// CampaignStore persiste campanhas.
type CampaignStore interface {
	SaveCampaign(c *campaign.Campaign) error
	LoadCampaign(id string) (*campaign.Campaign, error)
	ListCampaigns() ([]string, error)
}

// CharacterStore persiste personagens.
type CharacterStore interface {
	SaveCharacter(c *character.Character) error
	LoadCharacter(id string) (*character.Character, error)
	ListCharacters() ([]string, error)
}

// FileStore implementa CampaignStore e CharacterStore em disco.
type FileStore struct {
	root string
}

var (
	_ CampaignStore  = (*FileStore)(nil)
	_ CharacterStore = (*FileStore)(nil)
)

// NewFileStore cria (se preciso) a estrutura de diretórios em root.
func NewFileStore(root string) (*FileStore, error) {
	for _, d := range []string{"campaigns", "characters"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) path(kind, id string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("storage: id inválido %q", id)
	}
	return filepath.Join(s.root, kind, id+".json"), nil
}

func (s *FileStore) save(kind, id string, v any) error {
	p, err := s.path(kind, id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (s *FileStore) load(kind, id string, v any) error {
	p, err := s.path(kind, id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, kind, id)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *FileStore) list(kind string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, kind))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(n, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// SaveCampaign implementa CampaignStore.
func (s *FileStore) SaveCampaign(c *campaign.Campaign) error { return s.save("campaigns", c.ID, c) }

// LoadCampaign implementa CampaignStore.
func (s *FileStore) LoadCampaign(id string) (*campaign.Campaign, error) {
	var c campaign.Campaign
	if err := s.load("campaigns", id, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCampaigns implementa CampaignStore.
func (s *FileStore) ListCampaigns() ([]string, error) { return s.list("campaigns") }

// SaveCharacter implementa CharacterStore.
func (s *FileStore) SaveCharacter(c *character.Character) error {
	return s.save("characters", c.ID, c)
}

// LoadCharacter implementa CharacterStore.
func (s *FileStore) LoadCharacter(id string) (*character.Character, error) {
	var c character.Character
	if err := s.load("characters", id, &c); err != nil {
		return nil, err
	}
	if c.Inventory == nil {
		c.Inventory = map[string]int{}
	}
	return &c, nil
}

// ListCharacters implementa CharacterStore.
func (s *FileStore) ListCharacters() ([]string, error) { return s.list("characters") }
