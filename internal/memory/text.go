package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// foldAccent remove diacríticos do português sem depender de x/text.
var foldAccent = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ç': 'c', 'ñ': 'n',
}

// stopwords básicas do português, já sem acento.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		a o as os um uma uns umas de do da dos das d em no na nos nas num numa
		por pelo pela pelos pelas para pra pro com sem sob sobre ate apos entre
		e ou mas nem que se ja nao sim mais menos muito pouco tao como quando
		onde quem qual quais cujo este esta estes estas esse essa esses essas
		aquele aquela aqueles aquelas isto isso aquilo lhe lhes me te nos vos
		eu tu ele ela eles elas voce voces meu minha meus minhas seu sua seus
		suas nosso nossa ao aos foi era ser sao esta estao tem ha la ali aqui
	`) {
		m[w] = true
	}
	return m
}()

// normalize põe o texto em minúsculas e remove acentos.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if f, ok := foldAccent[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

// tokenize normaliza, quebra em palavras, descarta stopwords e tokens de
// uma letra, e aplica um stemming leve de plural — o bastante para
// "guardiões" casar com "guardião" e "promessas" com "promessa".
func tokenize(s string) []string {
	words := strings.FieldsFunc(normalize(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := words[:0]
	for _, w := range words {
		if len(w) < 2 || stopwords[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem reduz plurais regulares do português. É propositalmente simples e
// aplicado igual à consulta e às entradas, então erros se cancelam.
func stem(w string) string {
	if len(w) < 4 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "oes"), strings.HasSuffix(w, "aes"):
		return w[:len(w)-3] + "ao"
	case strings.HasSuffix(w, "ais"):
		return w[:len(w)-3] + "al"
	case strings.HasSuffix(w, "ns"):
		return w[:len(w)-2] + "m"
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

// EstimateTokens aproxima a contagem de tokens como runas/4 (arredondado
// para cima). É grosseiro, mas estável e independente de tokenizer.
func EstimateTokens(s string) int {
	return (utf8.RuneCountInString(s) + 3) / 4
}
