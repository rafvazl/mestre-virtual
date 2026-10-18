package memory

import (
	"math"
	"sort"
)

// Weights são os pesos do score final. Somam 1 para que o score fique em
// [0,1] e seja comparável entre consultas.
type Weights struct {
	Relevance  float64 // lexical (e vetorial, se houver Embedder)
	Ref        float64
	Recency    float64
	Importance float64
}

// DefaultWeights privilegia o que a consulta pede, depois as entidades em
// cena, e usa recência/importância como desempate "suave".
var DefaultWeights = Weights{Relevance: 0.50, Ref: 0.25, Recency: 0.15, Importance: 0.10}

// Parâmetros do ranking.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// RecencyHalfLife: a cada tantas sessões de distância o peso de
	// recência cai pela metade.
	RecencyHalfLife = 3.0
	// vectorShare é a fração da relevância dada ao vetor quando há Embedder.
	vectorShare = 0.5
)

// rank calcula o score de cada candidato. A fórmula, com todos os termos
// normalizados em [0,1]:
//
//	lex   = BM25(q, e) / max BM25 entre os candidatos
//	        BM25 = Σ_t∈q idf(t) · tf·(k1+1) / (tf + k1·(1 − b + b·|e|/avg|e|))
//	        idf  = ln(1 + (N − df + 0.5)/(df + 0.5))       k1=1.2, b=0.75
//	vec   = max(0, cos(q, e))                       (só com Embedder)
//	rel   = lex                         sem Embedder
//	      = 0.5·lex + 0.5·vec           com Embedder
//	ref   = |Refs(q) ∩ Refs(e)| / |Refs(q)|
//	rec   = 0.5^(max(0, sessãoAtual − sessão(e)) / 3)
//	imp   = Importance (0 ⇒ 0.5)
//
//	score = 0.50·rel + 0.25·ref + 0.15·rec + 0.10·imp
//
// Se a consulta tem texto ou Refs, entradas com rel = 0 e ref = 0 são
// descartadas (recência e importância sozinhas não tornam algo relevante).
// Sem texto nem Refs, o ranking vira "mais recentes e importantes".
// Empate: score desc, At desc, ID asc — totalmente determinístico.
func rank(cands []Entry, q Query, qVec []float32, w Weights) []Scored {
	qTerms := uniq(tokenize(q.Text))
	signal := len(qTerms) > 0 || len(q.Refs) > 0 || qVec != nil

	current := q.CurrentSession
	if current == 0 {
		for _, e := range cands {
			current = max(current, e.SessionNumber)
		}
	}

	// Estatísticas do corpus (os próprios candidatos filtrados).
	docs := make([][]string, len(cands))
	df := map[string]int{}
	total := 0
	for i, e := range cands {
		docs[i] = tokenize(e.Text)
		total += len(docs[i])
		for _, t := range uniq(docs[i]) {
			df[t]++
		}
	}
	n := float64(len(cands))
	avgLen := 1.0
	if len(cands) > 0 && total > 0 {
		avgLen = float64(total) / n
	}

	lex := make([]float64, len(cands))
	maxLex := 0.0
	for i, doc := range docs {
		tf := map[string]int{}
		for _, t := range doc {
			tf[t]++
		}
		for _, t := range qTerms {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			d := float64(df[t])
			idf := math.Log(1 + (n-d+0.5)/(d+0.5))
			lex[i] += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(len(doc))/avgLen))
		}
		maxLex = max(maxLex, lex[i])
	}

	out := make([]Scored, 0, len(cands))
	for i, e := range cands {
		var b Breakdown
		if maxLex > 0 {
			b.Lexical = lex[i] / maxLex
		}
		rel := b.Lexical
		if qVec != nil {
			b.Vector = max(0, cosine(qVec, e.Embedding))
			rel = (1-vectorShare)*b.Lexical + vectorShare*b.Vector
		}
		if len(q.Refs) > 0 {
			hit := 0
			for _, r := range q.Refs {
				if hasRef(e.Refs, r) {
					hit++
				}
			}
			b.Ref = float64(hit) / float64(len(q.Refs))
		}
		if signal && rel == 0 && b.Ref == 0 {
			continue
		}
		age := float64(max(0, current-e.SessionNumber))
		b.Recency = math.Pow(0.5, age/RecencyHalfLife)
		b.Importance = e.importance()

		score := w.Relevance*rel + w.Ref*b.Ref + w.Recency*b.Recency + w.Importance*b.Importance
		out = append(out, Scored{Entry: e, Score: score, Breakdown: b})
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if !a.Entry.At.Equal(b.Entry.At) {
			return a.Entry.At.After(b.Entry.At)
		}
		return a.Entry.ID < b.Entry.ID
	})
	return out
}

func uniq(ts []string) []string {
	seen := make(map[string]bool, len(ts))
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
