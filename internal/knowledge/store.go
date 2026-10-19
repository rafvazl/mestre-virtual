package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound indica que a entidade referenciada não existe.
var ErrNotFound = errors.New("knowledge: não encontrado")

// Store é a interface de leitura da base de conhecimento.
type Store interface {
	Item(id string) (Item, error)
	Class(id string) (Class, error)
	Environment(id string) (Environment, error)
	NPC(id string) (NPCTemplate, error)
	// Exists informa se a referência aponta para algo existente.
	Exists(ref Ref) bool
	// List devolve os IDs de um tipo, ordenados.
	List(t RefType) []string
}

// FileStore carrega a base a partir de um diretório com a estrutura:
//
//	content/items/*.json
//	content/classes/*.json
//	content/environments/*.json
//	content/npcs/*.json
//
// Cada arquivo contém um objeto ou um array de objetos. Tudo é carregado
// em memória na criação; a base é pequena e imutável durante a sessão.
type FileStore struct {
	items        map[string]Item
	classes      map[string]Class
	environments map[string]Environment
	npcs         map[string]NPCTemplate
}

// NewFileStore carrega a base de root. Diretórios ausentes são aceitos.
func NewFileStore(root string) (*FileStore, error) {
	s := &FileStore{
		items:        map[string]Item{},
		classes:      map[string]Class{},
		environments: map[string]Environment{},
		npcs:         map[string]NPCTemplate{},
	}
	if err := load(filepath.Join(root, "items"), s.items, func(v Item) string { return v.ID }); err != nil {
		return nil, err
	}
	if err := load(filepath.Join(root, "classes"), s.classes, func(v Class) string { return v.ID }); err != nil {
		return nil, err
	}
	if err := load(filepath.Join(root, "environments"), s.environments, func(v Environment) string { return v.ID }); err != nil {
		return nil, err
	}
	if err := load(filepath.Join(root, "npcs"), s.npcs, func(v NPCTemplate) string { return v.ID }); err != nil {
		return nil, err
	}
	return s, s.validate()
}

func load[T any](dir string, dst map[string]T, id func(T) string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var list []T
		if trimmed := strings.TrimSpace(string(data)); strings.HasPrefix(trimmed, "[") {
			err = json.Unmarshal(data, &list)
		} else {
			var one T
			err = json.Unmarshal(data, &one)
			list = []T{one}
		}
		if err != nil {
			return fmt.Errorf("knowledge: %s: %w", path, err)
		}
		for _, v := range list {
			k := id(v)
			if k == "" {
				return fmt.Errorf("knowledge: %s: entrada sem id", path)
			}
			if _, dup := dst[k]; dup {
				return fmt.Errorf("knowledge: %s: id duplicado %q", path, k)
			}
			dst[k] = v
		}
	}
	return nil
}

// validate garante integridade referencial (ex.: itens iniciais existem).
func (s *FileStore) validate() error {
	var errs []error
	for _, c := range s.classes {
		for _, it := range c.StartingItems {
			if _, ok := s.items[it]; !ok {
				errs = append(errs, fmt.Errorf("classe %q: item inicial %q inexistente", c.ID, it))
			}
		}
	}
	for _, n := range s.npcs {
		for _, it := range n.Loot {
			if _, ok := s.items[it]; !ok {
				errs = append(errs, fmt.Errorf("npc %q: loot %q inexistente", n.ID, it))
			}
		}
	}
	for _, e := range s.environments {
		for _, ex := range e.Exits {
			if _, ok := s.environments[ex]; !ok {
				errs = append(errs, fmt.Errorf("ambiente %q: saída %q inexistente", e.ID, ex))
			}
		}
	}
	return errors.Join(errs...)
}

func get[T any](m map[string]T, t RefType, id string) (T, error) {
	v, ok := m[id]
	if !ok {
		var zero T
		return zero, fmt.Errorf("%w: %s", ErrNotFound, Ref{Type: t, ID: id})
	}
	return v, nil
}

// Item implementa Store.
func (s *FileStore) Item(id string) (Item, error) { return get(s.items, RefItem, id) }

// Class implementa Store.
func (s *FileStore) Class(id string) (Class, error) { return get(s.classes, RefClass, id) }

// Environment implementa Store.
func (s *FileStore) Environment(id string) (Environment, error) {
	return get(s.environments, RefEnvironment, id)
}

// NPC implementa Store.
func (s *FileStore) NPC(id string) (NPCTemplate, error) { return get(s.npcs, RefNPC, id) }

// Exists implementa Store.
func (s *FileStore) Exists(ref Ref) bool {
	switch ref.Type {
	case RefItem:
		_, ok := s.items[ref.ID]
		return ok
	case RefClass:
		_, ok := s.classes[ref.ID]
		return ok
	case RefEnvironment:
		_, ok := s.environments[ref.ID]
		return ok
	case RefNPC:
		_, ok := s.npcs[ref.ID]
		return ok
	}
	return false
}

// List implementa Store.
func (s *FileStore) List(t RefType) []string {
	var ids []string
	switch t {
	case RefItem:
		ids = keys(s.items)
	case RefClass:
		ids = keys(s.classes)
	case RefEnvironment:
		ids = keys(s.environments)
	case RefNPC:
		ids = keys(s.npcs)
	}
	sort.Strings(ids)
	return ids
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
