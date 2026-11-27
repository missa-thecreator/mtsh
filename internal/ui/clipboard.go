package ui

import (
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/clipboard"
	"mtsh/internal/session"
)

// Copiar e colar nas abas de sessão.
//
// Como o mtsh usa o mouse (abas, cards, rodinha), o terminal de fora não
// seleciona mais texto. Então, na aba de sessão:
//   - arrastar com o botão esquerdo só marca o texto;
//   - botão direito (ou do meio) com texto marcado copia e desmarca; sem
//     nada marcado, cola o que está na área de transferência (como no
//     Windows Terminal);
//   - ctrl+v / ctrl+shift+v do terminal continuam colando (ver sessionKey).

type copiedMsg struct {
	chars int
	err   error
}

type pastedMsg struct {
	s    *session.Session
	text string
	err  error
}

// sessionOrigin: canto de cima da área da sessão na tela. A barra lateral
// ocupa sideW() colunas, mais 1 de margem; em cima fica a linha das abas.
const sessionOriginY = 2 // + barra de título da sessão

// sessionMouse trata o mouse na área da sessão. ok = false deixa o clique pro
// resto da tela (abas, barra lateral, rodinha).
func (m *Model) sessionMouse(s *session.Session, msg tea.MouseMsg) (cmd tea.Cmd, ok bool) {
	cols, rows := m.termSize()
	x, y := msg.X-(m.sideW()+1), msg.Y-sessionOriginY
	// a coluna de margem à esquerda conta como a primeira coluna
	inside := x >= -1 && x < cols && y >= 0 && y < rows

	switch {
	case m.dragging && msg.Action == tea.MouseActionMotion:
		m.dragX, m.dragY = x, y
		s.ExtendSelection(s.PointAt(x, y))
		if m.autoRoll {
			return nil, true // o tique já está rolando
		}
		return m.dragRoll(s), true

	case m.dragging && msg.Action == tea.MouseActionRelease:
		m.dragging = false // o tique de rolagem para sozinho
		if !s.HasSelection() {
			s.ClearSelection() // foi só um clique
		}
		return nil, true // marcado; copia no botão direito

	case !inside || msg.Action != tea.MouseActionPress:
		return nil, false

	case msg.Button == tea.MouseButtonLeft:
		m.dragging = true
		s.StartSelection(s.PointAt(x, y))
		return nil, true

	case msg.Button == tea.MouseButtonRight, msg.Button == tea.MouseButtonMiddle:
		// como no Windows Terminal: com texto marcado, copia e desmarca; sem nada marcado, cola
		if s.HasSelection() {
			text := s.SelectionText()
			s.ClearSelection()
			return copyCmd(text), true
		}
		if done, _ := s.Exited(); done {
			return nil, true
		}
		return pasteCmd(s), true
	}
	return nil, false
}

// dragRollMsg é o tique que rola a aba enquanto o mouse fica parado fora dela
// (acima ou abaixo) no meio de um arraste.
type dragRollMsg struct{ s *session.Session }

// dragRoll rola na direção em que o mouse saiu da aba e agenda o próximo
// tique. Para quando o arraste acaba ou o mouse volta pra dentro. Começa com
// 1 linha por tique (60/s, dá pra marcar com precisão) e ganha mais 1 a cada
// ¼ de segundo, até 20 por tique.
func (m *Model) dragRoll(s *session.Session) tea.Cmd {
	_, rows := m.termSize()
	if !m.dragging || s.AltScreen() || m.dragY >= 0 && m.dragY < rows {
		m.rollN = 0
		return nil
	}
	n := min(1+m.rollN/15, 20)
	if m.dragY >= rows {
		n = -n
	}
	s.ScrollBy(n)
	s.ExtendSelection(s.PointAt(m.dragX, m.dragY))
	m.autoRoll = true
	m.rollN++
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg { return dragRollMsg{s} })
}

func copyCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return copiedMsg{chars: utf8.RuneCountInString(text), err: clipboard.Copy(text, os.Stdout)}
	}
}

func pasteCmd(s *session.Session) tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.Paste()
		return pastedMsg{s: s, text: text, err: err}
	}
}

func (m *Model) onCopied(msg copiedMsg) {
	if msg.err != nil {
		m.say("não deu pra copiar: "+msg.err.Error(), true)
		return
	}
	m.say(fmt.Sprintf("copiado (%d caracteres)", msg.chars), false)
}

func (m *Model) onPasted(msg pastedMsg) {
	switch {
	case errors.Is(msg.err, clipboard.ErrUnavailable):
		m.say("não deu pra ler a área de transferência aqui · cole com ctrl+shift+v", true)
	case msg.err != nil:
		m.say("não deu pra colar: "+msg.err.Error(), true)
	case m.indexOf(msg.s) == 0:
		// a aba fechou enquanto lia a área de transferência
	default:
		m.toast = ""
		msg.s.ClearSelection()
		msg.s.Paste(msg.text)
	}
}
