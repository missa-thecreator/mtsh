// Package session roda o ssh num pseudo-terminal (PTY) e mantém uma cópia
// da tela do servidor num emulador de terminal (vt10x), pra o mtsh poder
// desenhar a sessão dentro de uma aba.
package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/creack/pty"
	"mtsh/internal/vt10x"
)

type Session struct {
	Name string // nome do servidor

	cmd  *exec.Cmd
	pty  *os.File
	term vt10x.Terminal

	updates chan struct{} // sinaliza saída nova; fechado quando o ssh termina

	mu      sync.Mutex
	exited  bool
	exitErr error
	offset  int // quantas linhas acima do fim a pessoa rolou (0 = no fim)
}

// Start roda cmd num PTY do tamanho dado.
func Start(name string, cmd *exec.Cmd, cols, rows int) (*Session, error) {
	cols, rows = max(cols, 10), max(rows, 3)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("não deu pra abrir o terminal: %w", err)
	}
	s := &Session{
		Name:    name,
		cmd:     cmd,
		pty:     f,
		updates: make(chan struct{}, 1),
		// respostas do terminal (ex: "qual seu tamanho?") voltam pro PTY
		term: vt10x.New(vt10x.WithWriter(f), vt10x.WithSize(cols, rows)),
	}
	go s.readLoop()
	return s, nil
}

func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	var pending []byte // pedaço de caractere UTF-8 que chegou cortado
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			data := append(pending, buf[:n]...)
			cut := incompleteTail(data)
			before := s.pushed()
			s.term.Write(data[:cut])
			// rolado pra cima? mantém parado o trecho que a pessoa está lendo
			if added := s.pushed() - before; added > 0 {
				s.mu.Lock()
				if s.offset > 0 {
					s.offset += added
				}
				s.mu.Unlock()
			}
			pending = append([]byte(nil), data[cut:]...)
			select {
			case s.updates <- struct{}{}:
			default: // já tem um aviso pendente; a tela é lida inteira de qualquer jeito
			}
		}
		if err != nil {
			break
		}
	}
	werr := s.cmd.Wait()
	s.mu.Lock()
	s.exited, s.exitErr = true, werr
	s.mu.Unlock()
	s.pty.Close()
	close(s.updates)
}

// incompleteTail devolve onde termina o último caractere UTF-8 completo.
func incompleteTail(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-4; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}

// Updates avisa quando chega saída nova; é fechado quando o ssh termina.
func (s *Session) Updates() <-chan struct{} { return s.updates }

// Exited diz se o ssh terminou e com qual erro (nil = saiu com código 0).
func (s *Session) Exited() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited, s.exitErr
}

// ExitCode devolve o código de saída do ssh (-1 se não souber).
func (s *Session) ExitCode() int {
	_, err := s.Exited()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Write manda o que foi digitado pro servidor.
func (s *Session) Write(b []byte) {
	if done, _ := s.Exited(); done || len(b) == 0 {
		return
	}
	s.pty.Write(b)
}

// AppCursor diz se o programa remoto pediu setas no modo "aplicação" (vim, less…).
func (s *Session) AppCursor() bool { return s.term.Mode()&vt10x.ModeAppCursor != 0 }

// Resize ajusta o PTY e o emulador ao tamanho da aba.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 10), max(rows, 3)
	if c, r := s.term.Size(); c == cols && r == rows {
		return
	}
	s.term.Resize(cols, rows)
	if done, _ := s.Exited(); !done {
		pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}

// Close encerra o ssh (se ainda estiver rodando).
func (s *Session) Close() {
	if done, _ := s.Exited(); done {
		return
	}
	if s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGHUP)
	}
	s.pty.Close()
}

// ---- desenho ----

// atributos do vt10x (as constantes dele não são exportadas)
const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
	attrItalic    = 1 << 4
)

type style struct {
	fg, bg                           vt10x.Color
	bold, italic, underline, reverse bool
}

func (s *Session) pushed() int {
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.HistoryPushed()
}

// ---- rolagem ----

// AltScreen diz se o programa remoto está na tela alternativa (vim, htop,
// less…). Nela não há histórico: a rodinha vira setas pro programa.
func (s *Session) AltScreen() bool { return s.term.Mode()&vt10x.ModeAltScreen != 0 }

// ScrollBy rola o histórico: n > 0 sobe, n < 0 desce.
func (s *Session) ScrollBy(n int) {
	s.term.Lock()
	hl := s.term.HistoryLen()
	s.term.Unlock()
	s.mu.Lock()
	s.offset = max(0, min(s.offset+n, hl))
	s.mu.Unlock()
}

// ScrollToBottom volta pro fim (a tela atual).
func (s *Session) ScrollToBottom() {
	s.mu.Lock()
	s.offset = 0
	s.mu.Unlock()
}

// Scroll diz quanto está rolado e o tamanho do histórico.
func (s *Session) Scroll() (offset, history int) {
	s.term.Lock()
	hl := s.term.HistoryLen()
	s.term.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return min(s.offset, hl), hl
}

// Lines devolve o que aparece na aba, uma string por linha, com cores ANSI:
// a tela atual ou, se a pessoa rolou pra cima, um trecho do histórico.
func (s *Session) Lines(showCursor bool) []string {
	s.mu.Lock()
	off := s.offset
	s.mu.Unlock()

	s.term.Lock()
	defer s.term.Unlock()
	cols, rows := s.term.Size()
	hl := s.term.HistoryLen()
	off = min(off, hl)
	cur := s.term.Cursor()
	cursorOn := showCursor && off == 0 && s.term.CursorVisible()
	blank := vt10x.Glyph{FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}

	out := make([]string, rows)
	var b strings.Builder
	for y := 0; y < rows; y++ {
		// histórico e tela formam uma lista só; a janela começa "off" linhas acima do fim
		idx := hl - off + y
		var hist []vt10x.Glyph
		sy := idx - hl
		if idx < hl {
			hist = s.term.HistoryLine(idx)
		}
		b.Reset()
		prev := style{fg: vt10x.DefaultFG, bg: vt10x.DefaultBG}
		for x := 0; x < cols; x++ {
			g := blank
			switch {
			case idx < hl && x < len(hist):
				g = hist[x]
			case idx >= hl:
				g = s.term.Cell(x, sy)
			}
			st := style{fg: g.FG, bg: g.BG,
				bold: g.Mode&attrBold != 0, italic: g.Mode&attrItalic != 0,
				underline: g.Mode&attrUnderline != 0, reverse: g.Mode&attrReverse != 0}
			if cursorOn && x == cur.X && sy == cur.Y {
				st.reverse = !st.reverse
			}
			if st != prev {
				b.WriteString(sgr(st))
				prev = st
			}
			ch := g.Char
			if ch == 0 || ch < ' ' {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteString("\x1b[0m")
		out[y] = b.String()
	}
	return out
}

func sgr(st style) string {
	var p []string
	p = append(p, "0")
	if st.bold {
		p = append(p, "1")
	}
	if st.italic {
		p = append(p, "3")
	}
	if st.underline {
		p = append(p, "4")
	}
	if st.reverse {
		p = append(p, "7")
	}
	if c := colorCode(st.fg, false); c != "" {
		p = append(p, c)
	}
	if c := colorCode(st.bg, true); c != "" {
		p = append(p, c)
	}
	return "\x1b[" + strings.Join(p, ";") + "m"
}

func colorCode(c vt10x.Color, bg bool) string {
	base, bright, ext := 30, 90, 38
	if bg {
		base, bright, ext = 40, 100, 48
	}
	switch {
	case c >= vt10x.DefaultFG: // DefaultFG, DefaultBG, DefaultCursor
		return ""
	case c < 8:
		return fmt.Sprint(base + int(c))
	case c < 16:
		return fmt.Sprint(bright + int(c) - 8)
	case c < 256:
		return fmt.Sprintf("%d;5;%d", ext, c)
	default: // cor RGB (24 bits)
		return fmt.Sprintf("%d;2;%d;%d;%d", ext, (c>>16)&0xff, (c>>8)&0xff, c&0xff)
	}
}
