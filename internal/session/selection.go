package session

import (
	"strings"

	"mtsh/internal/vt10x"
)

// Seleção de texto com o mouse e colagem.
//
// O mtsh liga o modo de mouse do terminal (pra abas, cards e rodinha), então o
// terminal de fora não consegue mais selecionar texto. A seleção é feita aqui,
// como no tmux: arrastar marca o trecho, e SelectionText devolve o texto.
//
// As linhas são guardadas num número "estável": a linha 0 é a primeira que já
// passou pelo terminal, contando as que saíram do histórico pelo limite. Assim
// o trecho marcado não escorrega quando chega saída nova.

// Point é uma célula: coluna X na linha estável Line.
type Point struct{ X, Line int }

func (p Point) before(q Point) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.X < q.X)
}

// base é o número estável da linha 0 do histórico atual (precisa do lock do term).
func (s *Session) base() int { return s.term.HistoryPushed() - s.term.HistoryLen() }

// PointAt converte uma célula da aba (x, y a partir do canto de cima) em Point,
// levando em conta quanto a pessoa rolou.
func (s *Session) PointAt(x, y int) Point {
	s.mu.Lock()
	off := s.offset
	s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	cols, rows := s.term.Size()
	hl := s.term.HistoryLen()
	off = min(off, hl)
	x, y = max(0, min(x, cols-1)), max(0, min(y, rows-1))
	return Point{X: x, Line: s.base() + hl - off + y}
}

// StartSelection começa uma seleção em p (ainda vazia).
func (s *Session) StartSelection(p Point) {
	s.mu.Lock()
	s.selOn, s.selA, s.selB = true, p, p
	s.mu.Unlock()
}

// ExtendSelection move a ponta solta da seleção até p.
func (s *Session) ExtendSelection(p Point) {
	s.mu.Lock()
	if s.selOn {
		s.selB = p
	}
	s.mu.Unlock()
}

// ClearSelection apaga a seleção.
func (s *Session) ClearSelection() {
	s.mu.Lock()
	s.selOn = false
	s.mu.Unlock()
}

// HasSelection diz se tem algum trecho marcado (mais que um clique parado).
func (s *Session) HasSelection() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selOn && s.selA != s.selB
}

// selRange devolve o começo e o fim da seleção, em ordem (precisa de s.mu).
func (s *Session) selRange() (from, to Point, ok bool) {
	if !s.selOn || s.selA == s.selB {
		return Point{}, Point{}, false
	}
	from, to = s.selA, s.selB
	if to.before(from) {
		from, to = to, from
	}
	return from, to, true
}

// selected diz se a célula (x, linha estável) está dentro de [from, to].
func selected(x, line int, from, to Point) bool {
	if line < from.Line || line > to.Line {
		return false
	}
	if line == from.Line && x < from.X {
		return false
	}
	if line == to.Line && x > to.X {
		return false
	}
	return true
}

// row devolve as células da linha estável line, ou nil se ela já não existe
// mais (precisa do lock do term).
func (s *Session) row(line int) []vt10x.Glyph {
	cols, rows := s.term.Size()
	idx := line - s.base()
	hl := s.term.HistoryLen()
	switch {
	case idx < 0 || idx >= hl+rows:
		return nil
	case idx < hl:
		return s.term.HistoryLine(idx)
	}
	out := make([]vt10x.Glyph, cols)
	for x := range out {
		out[x] = s.term.Cell(x, idx-hl)
	}
	return out
}

// SelectionText devolve o texto marcado. Linhas que o terminal quebrou por
// falta de espaço voltam a ser uma só; espaços no fim de cada linha somem.
func (s *Session) SelectionText() string {
	s.mu.Lock()
	from, to, ok := s.selRange()
	s.mu.Unlock()
	if !ok {
		return ""
	}
	s.term.Lock()
	defer s.term.Unlock()

	var b strings.Builder
	for line := from.Line; line <= to.Line; line++ {
		cells := s.row(line)
		x0, x1 := 0, len(cells)-1
		if line == from.Line {
			x0 = from.X
		}
		if line == to.Line {
			x1 = min(x1, to.X)
		}
		var l strings.Builder
		wrapped := false
		for x := x0; x <= x1 && x < len(cells); x++ {
			ch := cells[x].Char
			if ch == 0 || ch < ' ' {
				ch = ' '
			}
			l.WriteRune(ch)
			wrapped = cells[x].Mode&attrWrap != 0
		}
		text := l.String()
		if !wrapped || x1 < len(cells)-1 {
			text = strings.TrimRight(text, " ")
			wrapped = false
		}
		b.WriteString(text)
		if line < to.Line && !wrapped {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Paste manda um texto colado pro servidor. Quebras de linha viram enter
// (\r), como num terminal. Se o programa remoto pediu "bracketed paste" (bash,
// zsh, vim…), o texto vai entre \e[200~ e \e[201~: assim um trecho com várias
// linhas não sai executando comando por comando.
func (s *Session) Paste(text string) {
	if text == "" {
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	if s.mode()&vt10x.ModeBracketedPaste != 0 {
		// um fim de colagem no meio do texto encerraria a colagem antes da hora
		text = strings.ReplaceAll(text, "\x1b[201~", "")
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	s.ScrollToBottom()
	s.Write([]byte(text))
}
