package game

import (
	"fmt"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/campaign"
	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
)

// systemPrompt define o papel do mestre e o contrato de saída. O %d é a
// magnitude máxima aceita para HP_CHANGE.
const systemPrompt = `Você é o mestre de uma sessão de RPG de mesa. Narre em português, em segunda pessoa, de forma viva e concisa (no máximo três parágrafos), reagindo à ação do jogador e ao contexto fornecido.

Você NÃO altera o estado do jogo. Você apenas PROPÕE efeitos mecânicos; o sistema valida cada um e aplica só os válidos. Use somente IDs de personagens e de itens que aparecem no contexto.

Responda SEMPRE exatamente neste formato, com cada marcador sozinho na linha:

[NARRATIVE]
<narração para os jogadores>
[MECHANICS]
<zero ou mais linhas de efeito, uma por linha>

Linhas de efeito aceitas (nada além delas na seção [MECHANICS]):
HP_CHANGE target=<id_personagem> amount=<inteiro; negativo = dano, positivo = cura>
ITEM_GAINED target=<id_personagem> item=<id_item> [qty=<inteiro positivo, padrão 1>]
ITEM_LOST target=<id_personagem> item=<id_item> [qty=<inteiro positivo, padrão 1>]

Exemplos:
HP_CHANGE target=aria amount=-3
ITEM_GAINED target=aria item=moeda-ouro qty=5
ITEM_LOST target=aria item=pocao-cura

Regras:
- Se nada mecânico acontecer, deixe a seção [MECHANICS] vazia.
- |amount| de HP_CHANGE deve ser no máximo %d.
- ITEM_LOST só para itens que o personagem tem, na quantidade que tem.
- Nunca escreva os marcadores dentro da narração.`

// buildSystem devolve o system prompt com os limites configurados.
func buildSystem(maxHPDelta int) string {
	return fmt.Sprintf(systemPrompt, maxHPDelta)
}

// memoryHeader abre o bloco de memória (memory.ContextBuilder.Header) logo
// abaixo do título da seção.
const memoryHeader = "(registros de sessões anteriores e desta, relevantes para a ação, em ordem cronológica)"

// promptData é o conteúdo variável do prompt de um turno.
type promptData struct {
	env     knowledge.Environment
	party   []*character.Character
	memory  string // bloco de memory.ContextBuilder; "" = sem a seção
	history []campaign.LogEntry
	actor   *character.Character
	input   string
}

// buildPrompt monta o conteúdo do turno: ambiente, grupo, itens conhecidos,
// memória da aventura, histórico recente e, por último, a ação do jogador.
//
// A ação fica sempre na última linha: além de ser o que o modelo deve
// responder, é o que o EchoProvider ecoa no modo offline.
func buildPrompt(ks knowledge.Store, d promptData) string {
	var b strings.Builder
	env := d.env

	b.WriteString("## Ambiente atual\n")
	fmt.Fprintf(&b, "%s (%s): %s\n", env.Name, env.ID, env.Description)
	if len(env.Hazards) > 0 {
		fmt.Fprintf(&b, "Perigos: %s\n", strings.Join(env.Hazards, ", "))
	}
	if len(env.Exits) > 0 {
		fmt.Fprintf(&b, "Saídas: %s\n", strings.Join(env.Exits, ", "))
	}

	b.WriteString("\n## Grupo\n")
	for _, c := range d.party {
		fmt.Fprintf(&b, "- %s\n", describeCharacter(ks, c))
	}

	if ids := ks.List(knowledge.RefItem); len(ids) > 0 {
		b.WriteString("\n## Itens existentes no mundo (id: nome)\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "- %s: %s\n", id, itemName(ks, id))
		}
	}

	if m := strings.TrimSpace(d.memory); m != "" {
		b.WriteString("\n## Memória da aventura\n")
		b.WriteString(m)
		b.WriteString("\n")
	}

	if len(d.history) > 0 {
		b.WriteString("\n## Histórico recente\n")
		for _, e := range d.history {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", e.Kind, e.Actor, oneLine(e.Text))
		}
	}

	b.WriteString("\n## Ação do jogador\n")
	fmt.Fprintf(&b, "%s (%s): %s", d.actor.Name, d.actor.ID, oneLine(d.input))
	return b.String()
}

// describeCharacter resume PV e inventário de um personagem em uma linha.
func describeCharacter(ks knowledge.Store, c *character.Character) string {
	return fmt.Sprintf("%s (id %s, classe %s) PV %d/%d; inventário: %s",
		c.Name, c.ID, c.ClassID, c.HP, c.MaxHP, describeInventory(ks, c))
}

// describeInventory lista os itens como "Nome [id] xN", em ordem de ID.
func describeInventory(ks knowledge.Store, c *character.Character) string {
	ids := c.Items()
	if len(ids) == 0 {
		return "vazio"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s [%s] x%d", itemName(ks, id), id, c.Inventory[id]))
	}
	return strings.Join(parts, ", ")
}

// itemName devolve o nome do item, ou o próprio ID se não estiver na base.
func itemName(ks knowledge.Store, id string) string {
	if it, err := ks.Item(id); err == nil && it.Name != "" {
		return it.Name
	}
	return id
}

// oneLine colapsa espaços e quebras de linha. Impede que texto do jogador (ou
// do histórico) forme linhas soltas no prompt.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
