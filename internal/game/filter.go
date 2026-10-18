package game

import (
	"io"
	"strings"
)

// Marcadores do contrato de saída (comparação sem diferenciar maiúsculas,
// igual a llm.ParseResponse).
const (
	markerNarrative = "[NARRATIVE]"
	markerMechanics = "[MECHANICS]"
)

// narrationFilter recebe o texto do LLM em pedaços arbitrários e repassa ao
// jogador apenas a narrativa, à medida que chega.
//
// Um marcador só vale se ocupar a linha inteira, então a decisão é tomada por
// linha: enquanto a linha corrente ainda pode virar um marcador ("[MEC"), ela
// fica retida; assim que deixa de poder, é liberada e o resto da linha segue
// direto. Tudo que está na seção [MECHANICS] é descartado. As regras espelham
// llm.ParseResponse para que o jogador veja exatamente a narrativa parseada.
type narrationFilter struct {
	w         io.Writer
	mechanics bool            // dentro da seção [MECHANICS]
	pending   strings.Builder // início da linha corrente, ainda retido
	streaming bool            // linha corrente já confirmada como narrativa
	started   bool            // já emitiu algum texto não vazio
	blank     strings.Builder // linhas em branco retidas até haver mais texto
	last      byte            // último byte emitido
}

func newNarrationFilter(w io.Writer) *narrationFilter { return &narrationFilter{w: w} }

// Write consome um pedaço do stream.
func (f *narrationFilter) Write(s string) {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			f.partial(s)
			return
		}
		f.partial(s[:i])
		f.endLine()
		s = s[i+1:]
	}
}

// Flush libera a linha retida no fim do stream e garante quebra de linha
// final se algo foi impresso.
func (f *narrationFilter) Flush() {
	if !f.streaming && !f.mechanics {
		if line := f.pending.String(); markerOf(line) == "" && strings.TrimSpace(line) != "" {
			f.emit(line)
		}
	}
	f.pending.Reset()
	f.streaming = false
	if f.started && f.last != '\n' {
		f.emit("\n")
	}
}

func (f *narrationFilter) partial(s string) {
	if s == "" {
		return
	}
	if f.streaming {
		f.emit(s)
		return
	}
	f.pending.WriteString(s)
	if f.mechanics || couldBeMarker(f.pending.String()) {
		return
	}
	f.streaming = true
	f.emit(f.pending.String())
	f.pending.Reset()
}

func (f *narrationFilter) endLine() {
	if f.streaming {
		f.streaming = false
		f.emit("\n")
		return
	}
	line := f.pending.String()
	f.pending.Reset()
	switch markerOf(line) {
	case markerNarrative:
		f.mechanics = false
	case markerMechanics:
		f.mechanics = true
	default:
		switch {
		case f.mechanics:
		case strings.TrimSpace(line) == "":
			// Linhas em branco só saem se vier mais narrativa depois delas
			// (o parser descarta as das pontas).
			if f.started {
				f.blank.WriteString(line + "\n")
			}
		default:
			f.emit(line + "\n")
		}
	}
}

func (f *narrationFilter) emit(s string) {
	if s == "" {
		return
	}
	if strings.TrimSpace(s) == "" && !f.started {
		return
	}
	f.started = true
	if f.blank.Len() > 0 {
		_, _ = io.WriteString(f.w, f.blank.String())
		f.blank.Reset()
	}
	// Erro de escrita na saída não interrompe o turno: a narrativa completa
	// fica registrada no log da sessão de qualquer forma.
	_, _ = io.WriteString(f.w, s)
	f.last = s[len(s)-1]
}

// markerOf devolve o marcador que a linha representa, ou "".
func markerOf(line string) string {
	switch t := strings.ToUpper(strings.TrimSpace(line)); t {
	case markerNarrative, markerMechanics:
		return t
	}
	return ""
}

// couldBeMarker informa se a linha parcial ainda pode terminar como marcador.
func couldBeMarker(partial string) bool {
	t := strings.ToUpper(strings.TrimSpace(partial))
	return strings.HasPrefix(markerNarrative, t) || strings.HasPrefix(markerMechanics, t)
}
