package game

import (
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

// refIndex encontra, num texto livre, entidades da base de conhecimento
// citadas pelo ID ou pelo nome (sem diferenciar maiúsculas). Alimenta as
// Refs da memória: "falo com o velho guardião" → npc:velho-guardiao.
//
// É um casamento de substrings simples e determinístico; não trata acentos
// nem sinônimos (o ranking da memória já é tolerante a isso).
type refIndex []refKey

type refKey struct {
	key string
	ref knowledge.Ref
}

func newRefIndex(ks knowledge.Store) refIndex {
	var ix refIndex
	add := func(t knowledge.RefType, id, name string) {
		r := knowledge.Ref{Type: t, ID: id}
		ix = append(ix, refKey{strings.ToLower(id), r})
		if name != "" && !strings.EqualFold(name, id) {
			ix = append(ix, refKey{strings.ToLower(name), r})
		}
	}
	for _, id := range ks.List(knowledge.RefNPC) {
		n, _ := ks.NPC(id)
		add(knowledge.RefNPC, id, n.Name)
	}
	for _, id := range ks.List(knowledge.RefItem) {
		it, _ := ks.Item(id)
		add(knowledge.RefItem, id, it.Name)
	}
	for _, id := range ks.List(knowledge.RefEnvironment) {
		env, _ := ks.Environment(id)
		add(knowledge.RefEnvironment, id, env.Name)
	}
	return ix
}

// Find devolve as Refs citadas em text, sem repetição, na ordem do índice.
func (ix refIndex) Find(text string) []knowledge.Ref {
	t := strings.ToLower(text)
	var out []knowledge.Ref
	for _, k := range ix {
		if strings.Contains(t, k.key) && !containsRef(out, k.ref) {
			out = append(out, k.ref)
		}
	}
	return out
}

func containsRef(refs []knowledge.Ref, r knowledge.Ref) bool {
	for _, x := range refs {
		if x == r {
			return true
		}
	}
	return false
}
