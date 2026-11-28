package ui

import (
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// cTextSel é o fundo do texto selecionado no editor SQL.
var cTextSel = lipgloss.CompleteColor{TrueColor: "#2E4A63", ANSI256: "24", ANSI: "4"}

// sqlEditor é o editor do SQL. Próprio (não o textarea do bubbles) porque
// precisa de seleção: shift+setas marcam um trecho e o F5 roda só ele.
// Não quebra linha: linha comprida rola para o lado.
type sqlEditor struct {
	lines    [][]rune
	row, col int // cursor; col em runas
	want     int // coluna pedida ao subir/descer por linhas mais curtas

	sel        bool // há seleção: vai de (aRow, aCol) até o cursor
	aRow, aCol int

	top, left int // primeira linha e primeira coluna (em células) à vista
	focused   bool

	placeholder string
}

func newSQLEditor(placeholder string) *sqlEditor {
	return &sqlEditor{lines: [][]rune{nil}, placeholder: placeholder}
}

func (e *sqlEditor) Focus()        { e.focused = true }
func (e *sqlEditor) Blur()         { e.focused = false }
func (e *sqlEditor) Focused() bool { return e.focused }

func (e *sqlEditor) Value() string {
	s := make([]string, len(e.lines))
	for i, l := range e.lines {
		s[i] = string(l)
	}
	return strings.Join(s, "\n")
}

// SetValue troca o texto todo e deixa o cursor no fim.
func (e *sqlEditor) SetValue(s string) {
	e.lines = [][]rune{nil}
	e.row, e.col, e.sel = 0, 0, false
	e.insert(s)
}

// CursorEnd vai pro fim do texto.
func (e *sqlEditor) CursorEnd() {
	e.sel = false
	e.row = len(e.lines) - 1
	e.col = len(e.lines[e.row])
	e.want = e.col
}

// InsertString digita s onde está o cursor (no lugar da seleção, se houver).
func (e *sqlEditor) InsertString(s string) { e.insert(s) }

// Offset é a posição do cursor no texto, em runas.
func (e *sqlEditor) Offset() int { return e.offsetOf(e.row, e.col) }

func (e *sqlEditor) offsetOf(row, col int) int {
	off := 0
	for _, l := range e.lines[:row] {
		off += len(l) + 1
	}
	return off + col
}

// selRange devolve o começo e o fim da seleção (linha, coluna) em ordem.
func (e *sqlEditor) selRange() (r0, c0, r1, c1 int) {
	r0, c0, r1, c1 = e.aRow, e.aCol, e.row, e.col
	if r1 < r0 || r1 == r0 && c1 < c0 {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	return
}

// HasSelection diz se tem um trecho (não vazio) selecionado.
func (e *sqlEditor) HasSelection() bool {
	return e.sel && (e.aRow != e.row || e.aCol != e.col)
}

// Selection devolve o texto selecionado e onde ele começa (em runas).
func (e *sqlEditor) Selection() (text string, start int, ok bool) {
	if !e.HasSelection() {
		return "", 0, false
	}
	r0, c0, r1, c1 := e.selRange()
	start, end := e.offsetOf(r0, c0), e.offsetOf(r1, c1)
	return string([]rune(e.Value())[start:end]), start, true
}

func (e *sqlEditor) ClearSelection() { e.sel = false }

func (e *sqlEditor) SelectAll() {
	e.sel, e.aRow, e.aCol = true, 0, 0
	e.CursorEnd()
	e.sel = true
}

// deleteSelection apaga o trecho selecionado; diz se havia algum.
func (e *sqlEditor) deleteSelection() bool {
	if !e.HasSelection() {
		e.sel = false
		return false
	}
	r0, c0, r1, c1 := e.selRange()
	tail := e.lines[r1][c1:]
	line := append(append([]rune{}, e.lines[r0][:c0]...), tail...)
	e.lines = append(e.lines[:r0+1], e.lines[r1+1:]...)
	e.lines[r0] = line
	e.row, e.col, e.want, e.sel = r0, c0, c0, false
	return true
}

func (e *sqlEditor) insert(s string) {
	e.deleteSelection()
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(s)
	parts := strings.Split(s, "\n")
	cur := e.lines[e.row]
	tail := append([]rune{}, cur[e.col:]...)
	first := append(append([]rune{}, cur[:e.col]...), []rune(parts[0])...)
	if len(parts) == 1 {
		e.lines[e.row] = append(first, tail...)
		e.col = len(first)
		e.want = e.col
		return
	}
	add := make([][]rune, 0, len(parts))
	add = append(add, first)
	for _, p := range parts[1 : len(parts)-1] {
		add = append(add, []rune(p))
	}
	last := []rune(parts[len(parts)-1])
	add = append(add, append(append([]rune{}, last...), tail...))
	rest := append([][]rune{}, e.lines[e.row+1:]...)
	e.lines = append(append(e.lines[:e.row], add...), rest...)
	e.row += len(parts) - 1
	e.col = len(last)
	e.want = e.col
}

// ---- movimento ----

func (e *sqlEditor) setCol(c int) { e.col, e.want = c, c }

func (e *sqlEditor) left1() {
	if e.col > 0 {
		e.setCol(e.col - 1)
	} else if e.row > 0 {
		e.row--
		e.setCol(len(e.lines[e.row]))
	}
}

func (e *sqlEditor) right1() {
	if e.col < len(e.lines[e.row]) {
		e.setCol(e.col + 1)
	} else if e.row < len(e.lines)-1 {
		e.row++
		e.setCol(0)
	}
}

func (e *sqlEditor) vert(d int) {
	r := max(0, min(len(e.lines)-1, e.row+d))
	if r == e.row {
		if d < 0 {
			e.setCol(0)
		} else {
			e.setCol(len(e.lines[e.row]))
		}
		return
	}
	e.row = r
	e.col = min(e.want, len(e.lines[r]))
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func (e *sqlEditor) wordLeft() {
	if e.col == 0 {
		e.left1()
		return
	}
	l, c := e.lines[e.row], e.col
	for c > 0 && !isWord(l[c-1]) {
		c--
	}
	for c > 0 && isWord(l[c-1]) {
		c--
	}
	e.setCol(c)
}

func (e *sqlEditor) wordRight() {
	l, c := e.lines[e.row], e.col
	if c == len(l) {
		e.right1()
		return
	}
	for c < len(l) && !isWord(l[c]) {
		c++
	}
	for c < len(l) && isWord(l[c]) {
		c++
	}
	e.setCol(c)
}

// move aplica um movimento; com shift estende a seleção, sem ele desfaz.
func (e *sqlEditor) move(name string, shift bool, page int) {
	if shift && !e.sel {
		e.sel, e.aRow, e.aCol = true, e.row, e.col
	}
	if !shift && e.HasSelection() && (name == "left" || name == "right") {
		// seta com seleção: o cursor vai pra ponta da seleção, como nos editores
		r0, c0, r1, c1 := e.selRange()
		if name == "left" {
			e.row, e.col = r0, c0
		} else {
			e.row, e.col = r1, c1
		}
		e.want, e.sel = e.col, false
		return
	}
	if !shift {
		e.sel = false
	}
	switch name {
	case "left":
		e.left1()
	case "right":
		e.right1()
	case "up":
		e.vert(-1)
	case "down":
		e.vert(1)
	case "pgup":
		e.vert(-page)
	case "pgdown":
		e.vert(page)
	case "home":
		e.setCol(0)
	case "end":
		e.setCol(len(e.lines[e.row]))
	case "wordleft":
		e.wordLeft()
	case "wordright":
		e.wordRight()
	case "top":
		e.row = 0
		e.setCol(0)
	case "bottom":
		e.row = len(e.lines) - 1
		e.setCol(len(e.lines[e.row]))
	}
}

// moves liga a tecla ao movimento e se ela estende a seleção.
var moves = map[string]struct {
	name  string
	shift bool
}{
	"left": {"left", false}, "right": {"right", false}, "up": {"up", false}, "down": {"down", false},
	"home": {"home", false}, "end": {"end", false}, "pgup": {"pgup", false}, "pgdown": {"pgdown", false},
	"ctrl+left": {"wordleft", false}, "ctrl+right": {"wordright", false},
	"alt+left": {"wordleft", false}, "alt+right": {"wordright", false},
	"ctrl+home": {"top", false}, "ctrl+end": {"bottom", false},
	"shift+left": {"left", true}, "shift+right": {"right", true}, "shift+up": {"up", true}, "shift+down": {"down", true},
	"shift+home": {"home", true}, "shift+end": {"end", true},
	"ctrl+shift+left": {"wordleft", true}, "ctrl+shift+right": {"wordright", true},
	"ctrl+shift+up": {"up", true}, "ctrl+shift+down": {"down", true},
	"ctrl+shift+home": {"top", true}, "ctrl+shift+end": {"bottom", true},
}

// Update trata uma tecla. page é quanto o pgup/pgdown anda.
func (e *sqlEditor) Update(k tea.KeyMsg, page int) {
	key := k.String()
	if mv, ok := moves[key]; ok {
		e.move(mv.name, mv.shift, page)
		return
	}
	switch {
	case key == "ctrl+a":
		e.SelectAll()
	case k.Type == tea.KeyRunes:
		e.insert(string(k.Runes)) // também o que vem colado
	case key == " ":
		e.insert(" ")
	case key == "enter":
		// mantém o recuo da linha de cima
		l := e.lines[e.row]
		n := 0
		for n < len(l) && l[n] == ' ' && n < e.col {
			n++
		}
		e.insert("\n" + strings.Repeat(" ", n))
	case key == "backspace":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.left1()
			e.deleteSelection()
		}
	case key == "delete":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.right1()
			e.deleteSelection()
		}
	case key == "ctrl+w", key == "alt+backspace":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.wordLeft()
			e.deleteSelection()
		}
	case key == "alt+delete", key == "alt+d":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.wordRight()
			e.deleteSelection()
		}
	case key == "ctrl+u":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.setCol(0)
			e.deleteSelection()
		}
	case key == "ctrl+k":
		if !e.deleteSelection() {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
			e.setCol(len(e.lines[e.row]))
			e.deleteSelection()
		}
	}
}

// ---- mouse ----

// cellCol converte uma coluna de célula (já somado o rolamento) em runa da linha.
func cellCol(l []rune, x int) int {
	w := 0
	for i, r := range l {
		rw := runewidth.RuneWidth(r)
		if x < w+rw {
			return i
		}
		w += rw
	}
	return len(l)
}

// gutter é a largura da coluna dos números de linha (com o espaço).
func (e *sqlEditor) gutter() int { return max(3, len(strconv.Itoa(len(e.lines)))) + 1 }

// Click põe o cursor na célula (x, y) do editor (relativa ao desenho).
// extend estende a seleção (arrastar com o botão apertado).
func (e *sqlEditor) Click(x, y int, extend bool) {
	row := max(0, min(len(e.lines)-1, e.top+y))
	col := cellCol(e.lines[row], max(0, x-e.gutter())+e.left)
	if extend {
		if !e.sel {
			e.sel, e.aRow, e.aCol = true, e.row, e.col
		}
	} else {
		e.sel = false
	}
	e.row = row
	e.setCol(col)
}

// Scroll rola as linhas sem mexer no cursor.
func (e *sqlEditor) Scroll(d, h int) {
	e.top = max(0, min(len(e.lines)-h, e.top+d))
}

// ---- desenho ----

// View desenha o editor em w colunas por h linhas.
func (e *sqlEditor) View(w, h int) []string {
	g := e.gutter()
	tw := max(1, w-g)
	// rola pra deixar o cursor à vista
	if e.row < e.top {
		e.top = e.row
	}
	if e.row >= e.top+h {
		e.top = e.row - h + 1
	}
	e.top = max(0, min(e.top, len(e.lines)-1))
	cx := runewidth.StringWidth(string(e.lines[e.row][:e.col]))
	if cx < e.left {
		e.left = cx
	}
	if cx >= e.left+tw {
		e.left = cx - tw + 1
	}

	num := lipgloss.NewStyle().Foreground(cFaint)
	numCur := lipgloss.NewStyle().Foreground(cAcc)
	text := lipgloss.NewStyle().Foreground(cFg)
	selSt := lipgloss.NewStyle().Foreground(cFg).Background(cTextSel)
	curSt := lipgloss.NewStyle().Foreground(cDark).Background(cAcc)
	if !e.focused {
		curSt = lipgloss.NewStyle()
	}
	sel := e.HasSelection()
	r0, c0, r1, c1 := e.selRange()
	inSel := func(row, col int) bool {
		if !sel || row < r0 || row > r1 {
			return false
		}
		return (row > r0 || col >= c0) && (row < r1 || col < c1)
	}

	out := make([]string, 0, h)
	empty := len(e.lines) == 1 && len(e.lines[0]) == 0
	for y := 0; y < h; y++ {
		row := e.top + y
		if row >= len(e.lines) {
			out = append(out, "")
			continue
		}
		var b strings.Builder
		ns := num
		if row == e.row {
			ns = numCur
		}
		b.WriteString(ns.Render(padLeft(strconv.Itoa(row+1), g-1) + " "))
		if empty && e.placeholder != "" {
			if e.focused {
				b.WriteString(curSt.Render(" "))
			}
			b.WriteString(lipgloss.NewStyle().Foreground(cFaint).Render(truncCells(e.placeholder, tw-1)))
			out = append(out, b.String())
			continue
		}
		// junta as runas em pedaços do mesmo estilo
		var run strings.Builder
		style := -1 // 0 texto, 1 seleção, 2 cursor
		flush := func() {
			if run.Len() == 0 {
				return
			}
			switch style {
			case 0:
				b.WriteString(text.Render(run.String()))
			case 1:
				b.WriteString(selSt.Render(run.String()))
			case 2:
				b.WriteString(curSt.Render(run.String()))
			}
			run.Reset()
		}
		put := func(s string, st int) {
			if st != style {
				flush()
				style = st
			}
			run.WriteString(s)
		}
		l := e.lines[row]
		x := 0
		used := 0
		for i := 0; i <= len(l) && used < tw; i++ {
			r := ' '
			if i < len(l) {
				r = l[i]
			}
			rw := runewidth.RuneWidth(r)
			if x < e.left {
				x += rw
				continue
			}
			x += rw
			st := 0
			if inSel(row, i) {
				st = 1
			}
			if row == e.row && i == e.col && e.focused {
				st = 2
			}
			if i == len(l) {
				// depois do fim da linha: só desenha o cursor ou a "quebra" selecionada
				if st == 0 {
					break
				}
				if st == 1 && row == r1 {
					break
				}
			}
			used += rw
			put(string(r), st)
		}
		flush()
		out = append(out, b.String())
	}
	// cada linha ocupa a largura toda: a zona do mouse vai de ponta a ponta
	for i, l := range out {
		if lw := lipgloss.Width(l); lw < w {
			out[i] = l + strings.Repeat(" ", w-lw)
		}
	}
	return out
}

func padLeft(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

func truncCells(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return runewidth.Truncate(s, n, "…")
}
