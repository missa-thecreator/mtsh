package ui

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/clipboard"
	"mtsh/internal/session"
)

// Copiar e colar nas abas de sessão.
//
// Como o mtsh usa o mouse (abas, cards, rodinha), o terminal de fora não
// seleciona mais texto. Então, na aba de sessão:
//   - arrastar com o botão esquerdo marca o texto e copia ao soltar;
//   - botão direito (ou do meio) cola o que está na área de transferência;
//     com texto marcado, ele só desmarca (como no Windows Terminal);
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
		// arrastou pra fora da aba: rola o histórico pra continuar marcando
		if !s.AltScreen() {
			switch {
			case y < 0:
				s.ScrollBy(1)
			case y >= rows:
				s.ScrollBy(-1)
			}
		}
		s.ExtendSelection(s.PointAt(x, y))
		return nil, true

	case m.dragging && msg.Action == tea.MouseActionRelease:
		m.dragging = false
		if !s.HasSelection() {
			s.ClearSelection() // foi só um clique
			return nil, true
		}
		return copyCmd(s.SelectionText()), true

	case !inside || msg.Action != tea.MouseActionPress:
		return nil, false

	case msg.Button == tea.MouseButtonLeft:
		m.dragging = true
		s.StartSelection(s.PointAt(x, y))
		return nil, true

	case msg.Button == tea.MouseButtonRight, msg.Button == tea.MouseButtonMiddle:
		// como no Windows Terminal: com texto marcado, o clique só desmarca (o
		// texto já foi copiado ao soltar); sem nada marcado, cola
		if s.HasSelection() {
			s.ClearSelection()
			return nil, true
		}
		if done, _ := s.Exited(); done {
			return nil, true
		}
		return pasteCmd(s), true
	}
	return nil, false
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
