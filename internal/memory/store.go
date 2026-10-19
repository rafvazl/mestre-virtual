package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mesma regra de ID de internal/storage: impede path traversal no nome
// do arquivo.
var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// fileVersion é a versão do formato em disco.
const fileVersion = 1

// campaignFile é o conteúdo de data/memory/<campaignID>.json.
type campaignFile struct {
	Version    int    `json:"version"`
	CampaignID string `json:"campaign_id"`
	// NextSeq gera IDs sequenciais e estáveis ("m-000001").
	NextSeq int     `json:"next_seq"`
	Entries []Entry `json:"entries"`
	// Archived guarda as entradas substituídas por resumos: nada se perde,
	// mas elas não participam mais do Recall.
	Archived []Entry `json:"archived,omitempty"`
}

// Store implementa Memory em RAM, com persistência opcional em JSON.
//
// É segura para uso concorrente: um único mutex protege todas as
// campanhas. A escrita em disco acontece sob o mutex, garantindo que o
// arquivo reflita sempre a última mutação (o volume de uma CLI de RPG não
// justifica nada mais elaborado).
type Store struct {
	mu        sync.Mutex
	dir       string // vazio = só memória
	campaigns map[string]*campaignFile
	embedder  Embedder
	weights   Weights
	now       func() time.Time
}

var _ Memory = (*Store)(nil)

// Option configura a Store.
type Option func(*Store)

// WithEmbedder liga a busca vetorial combinada ao ranking lexical.
func WithEmbedder(e Embedder) Option { return func(s *Store) { s.embedder = e } }

// WithWeights troca os pesos do ranking.
func WithWeights(w Weights) Option { return func(s *Store) { s.weights = w } }

// WithClock injeta o relógio (testes).
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// NewInMemory cria uma Store sem persistência.
func NewInMemory(opts ...Option) *Store {
	s := &Store{
		campaigns: map[string]*campaignFile{},
		weights:   DefaultWeights,
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Open cria uma Store persistida em <root>/memory/<campaignID>.json.
// Com root = "data", segue o mesmo layout de internal/storage.
// Cada campanha é carregada do disco no primeiro acesso.
func Open(root string, opts ...Option) (*Store, error) {
	dir := filepath.Join(root, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := NewInMemory(opts...)
	s.dir = dir
	return s, nil
}

// campaign devolve (carregando se preciso) o estado da campanha.
// Deve ser chamado com s.mu travado.
func (s *Store) campaign(id string) (*campaignFile, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("%w: campaign id %q", ErrInvalidEntry, id)
	}
	if c, ok := s.campaigns[id]; ok {
		return c, nil
	}
	c := &campaignFile{Version: fileVersion, CampaignID: id, NextSeq: 1}
	if s.dir != "" {
		data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			if err := json.Unmarshal(data, c); err != nil {
				return nil, fmt.Errorf("memory: %s corrompido: %w", id, err)
			}
			if c.NextSeq < 1 {
				c.NextSeq = 1
			}
		}
	}
	s.campaigns[id] = c
	return c, nil
}

// persist grava a campanha de forma atômica (temporário + rename), no
// mesmo padrão de internal/storage. Deve ser chamado com s.mu travado.
func (s *Store) persist(c *campaignFile) error {
	if s.dir == "" {
		return nil
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
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
	return os.Rename(tmp.Name(), filepath.Join(s.dir, c.CampaignID+".json"))
}

func (s *Store) validate(e *Entry) error {
	e.Text = strings.TrimSpace(e.Text)
	if e.Text == "" {
		return fmt.Errorf("%w: texto vazio", ErrInvalidEntry)
	}
	if e.SessionNumber < 0 {
		return fmt.Errorf("%w: sessão negativa", ErrInvalidEntry)
	}
	for _, r := range e.Refs {
		if !r.Type.Valid() || r.ID == "" {
			return fmt.Errorf("%w: ref %q", ErrInvalidEntry, r)
		}
	}
	if e.Kind == "" {
		e.Kind = KindEvent
	}
	if e.At.IsZero() {
		e.At = s.now()
	}
	e.At = e.At.UTC()
	e.Importance = max(0, min(e.Importance, 1))
	return nil
}

// Record implementa Memory. ID é sempre atribuído pela Store.
func (s *Store) Record(ctx context.Context, e Entry) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if err := s.validate(&e); err != nil {
		return Entry{}, err
	}
	// O embedding é calculado fora do lock (pode ser uma chamada de rede).
	// Falha do Embedder não impede o registro: a entrada só fica sem vetor.
	e.Embedding = nil
	if s.embedder != nil {
		if vs, err := s.embedder.Embed(ctx, []string{e.Text}); err == nil && len(vs) == 1 {
			e.Embedding = vs[0]
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.campaign(e.CampaignID)
	if err != nil {
		return Entry{}, err
	}
	e.ID = fmt.Sprintf("m-%06d", c.NextSeq)
	c.NextSeq++
	c.Entries = append(c.Entries, e)
	if err := s.persist(c); err != nil {
		// Desfaz para RAM e disco não divergirem.
		c.Entries = c.Entries[:len(c.Entries)-1]
		c.NextSeq--
		return Entry{}, err
	}
	return e, nil
}

// List implementa Memory: entradas ativas em ordem cronológica.
func (s *Store) List(ctx context.Context, campaignID string) ([]Entry, error) {
	return s.list(ctx, campaignID, false)
}

// Archived devolve as entradas já substituídas por resumos.
func (s *Store) Archived(ctx context.Context, campaignID string) ([]Entry, error) {
	return s.list(ctx, campaignID, true)
}

func (s *Store) list(ctx context.Context, campaignID string, archived bool) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	c, err := s.campaign(campaignID)
	var src []Entry
	if err == nil {
		src = c.Entries
		if archived {
			src = c.Archived
		}
		src = cloneEntries(src)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	sortChrono(src)
	return src, nil
}

// Recall implementa Memory. Veja rank para a fórmula.
func (s *Store) Recall(ctx context.Context, q Query) ([]Scored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.Limit < 0 || q.MinSession < 0 || q.MaxSession < 0 {
		return nil, fmt.Errorf("%w: limites negativos", ErrInvalidQuery)
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}

	s.mu.Lock()
	c, err := s.campaign(q.CampaignID)
	var cands []Entry
	if err == nil {
		for _, e := range c.Entries {
			if match(e, q) {
				cands = append(cands, e)
			}
		}
		cands = cloneEntries(cands)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Vetor da consulta fora do lock; se falhar, degrada para lexical.
	var qVec []float32
	if s.embedder != nil && strings.TrimSpace(q.Text) != "" {
		if vs, err := s.embedder.Embed(ctx, []string{q.Text}); err == nil && len(vs) == 1 {
			qVec = vs[0]
		}
	}

	out := rank(cands, q, qVec, s.weights)
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// match aplica os filtros duros da consulta.
func match(e Entry, q Query) bool {
	if q.MinSession > 0 && e.SessionNumber < q.MinSession {
		return false
	}
	if q.MaxSession > 0 && e.SessionNumber > q.MaxSession {
		return false
	}
	if len(q.Kinds) > 0 {
		ok := false
		for _, k := range q.Kinds {
			ok = ok || e.Kind == k
		}
		if !ok {
			return false
		}
	}
	if len(q.FilterRefs) > 0 {
		ok := false
		for _, r := range q.FilterRefs {
			ok = ok || hasRef(e.Refs, r)
		}
		if !ok {
			return false
		}
	}
	return true
}

// replace troca as entradas ids (as que ainda existirem) pelo resumo sum,
// arquivando-as. É a operação atômica usada pelo Compactor. Devolve o
// resumo gravado; se nenhuma das ids existir mais, não faz nada.
func (s *Store) replace(ctx context.Context, campaignID string, ids []string, sum Entry) (Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	sum.CampaignID = campaignID
	sum.Kind = KindSummary
	if err := s.validate(&sum); err != nil {
		return Entry{}, false, err
	}
	sum.Embedding = nil
	if s.embedder != nil {
		if vs, err := s.embedder.Embed(ctx, []string{sum.Text}); err == nil && len(vs) == 1 {
			sum.Embedding = vs[0]
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.campaign(campaignID)
	if err != nil {
		return Entry{}, false, err
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	prevEntries, prevArchived, prevSeq := c.Entries, c.Archived, c.NextSeq

	kept := make([]Entry, 0, len(c.Entries))
	archived := append([]Entry(nil), c.Archived...)
	sum.Summarizes = nil
	for _, e := range c.Entries {
		if drop[e.ID] {
			archived = append(archived, e)
			sum.Summarizes = append(sum.Summarizes, e.ID)
		} else {
			kept = append(kept, e)
		}
	}
	if len(sum.Summarizes) == 0 {
		return Entry{}, false, nil
	}
	sum.ID = fmt.Sprintf("m-%06d", c.NextSeq)
	c.NextSeq++
	c.Entries = append(kept, sum)
	c.Archived = archived
	if err := s.persist(c); err != nil {
		c.Entries, c.Archived, c.NextSeq = prevEntries, prevArchived, prevSeq
		return Entry{}, false, err
	}
	return sum, true, nil
}

func cloneEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	for i, e := range in {
		e.Refs = append(e.Refs[:0:0], e.Refs...)
		e.Summarizes = append(e.Summarizes[:0:0], e.Summarizes...)
		e.Embedding = append(e.Embedding[:0:0], e.Embedding...)
		out[i] = e
	}
	return out
}

func sortChrono(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.SessionNumber != b.SessionNumber {
			return a.SessionNumber < b.SessionNumber
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.ID < b.ID
	})
}
