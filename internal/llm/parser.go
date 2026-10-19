package llm

import (
	"fmt"
	"strconv"
	"strings"
)

// EffectType é o tipo de um efeito mecânico proposto pelo LLM.
type EffectType string

const (
	EffectHPChange   EffectType = "HP_CHANGE"
	EffectItemGained EffectType = "ITEM_GAINED"
	EffectItemLost   EffectType = "ITEM_LOST"
)

// StructuredEffect é um efeito mecânico já validado sintaticamente.
// O LLM só PROPÕE efeitos; quem aplica (ou rejeita) é o núcleo em Go.
type StructuredEffect struct {
	Type   EffectType
	Target string // ID do personagem/NPC afetado
	Amount int    // HP_CHANGE: delta; ITEM_*: quantidade
	ItemID string // ITEM_*: ID do item na base de conhecimento
}

// ParsedResponse separa narração de mecânica.
type ParsedResponse struct {
	Narrative string
	Effects   []StructuredEffect
}

// ParseError descreve uma linha de [MECHANICS] inválida.
type ParseError struct {
	Line int
	Text string
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("mechanics linha %d %q: %s", e.Line, e.Text, e.Msg)
}

// ParseResponse interpreta a saída do LLM no contrato:
//
//	[NARRATIVE]
//	texto livre...
//	[MECHANICS]
//	HP_CHANGE target=<id> amount=<int>
//	ITEM_GAINED target=<id> item=<id> [qty=<int>]
//	ITEM_LOST target=<id> item=<id> [qty=<int>]
//
// O parsing é determinístico. Se não houver marcadores, tudo é tratado como
// narrativa (degradação segura). Linhas de mecânica inválidas são coletadas
// em errs e ignoradas — nunca aplicadas.
func ParseResponse(raw string) (ParsedResponse, []error) {
	var (
		out     ParsedResponse
		errs    []error
		section = "narrative"
		narr    strings.Builder
	)
	// Divide por linhas sem bufio.Scanner: o Scanner tem limite de 64 KiB por
	// linha e, ao estourar, parava em silêncio — a resposta inteira (inclusive
	// [MECHANICS]) era descartada sem nenhum erro. "\r" final é removido para
	// aceitar CRLF, como fazia o ScanLines.
	for i, line := range strings.Split(raw, "\n") {
		lineNo := i + 1
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		switch strings.ToUpper(trimmed) {
		case "[NARRATIVE]":
			section = "narrative"
			continue
		case "[MECHANICS]":
			section = "mechanics"
			continue
		}
		if section == "narrative" {
			narr.WriteString(line)
			narr.WriteByte('\n')
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eff, err := parseEffect(trimmed)
		if err != nil {
			errs = append(errs, &ParseError{Line: lineNo, Text: trimmed, Msg: err.Error()})
			continue
		}
		out.Effects = append(out.Effects, eff)
	}
	out.Narrative = strings.TrimSpace(narr.String())
	return out, errs
}

func parseEffect(line string) (StructuredEffect, error) {
	fields := strings.Fields(line)
	eff := StructuredEffect{Type: EffectType(strings.ToUpper(fields[0]))}
	kv := map[string]string{}
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" || v == "" {
			return eff, fmt.Errorf("argumento malformado %q", f)
		}
		kv[strings.ToLower(k)] = v
	}
	eff.Target = kv["target"]
	if eff.Target == "" {
		return eff, fmt.Errorf("target obrigatório")
	}
	switch eff.Type {
	case EffectHPChange:
		n, err := strconv.Atoi(kv["amount"])
		if err != nil {
			return eff, fmt.Errorf("amount inválido")
		}
		eff.Amount = n
	case EffectItemGained, EffectItemLost:
		eff.ItemID = kv["item"]
		if eff.ItemID == "" {
			return eff, fmt.Errorf("item obrigatório")
		}
		eff.Amount = 1
		if q, ok := kv["qty"]; ok {
			n, err := strconv.Atoi(q)
			if err != nil || n <= 0 {
				return eff, fmt.Errorf("qty inválido")
			}
			eff.Amount = n
		}
	default:
		return eff, fmt.Errorf("tipo de efeito desconhecido")
	}
	return eff, nil
}
