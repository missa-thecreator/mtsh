package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

// modal é uma janela aberta por cima da tela principal.
type modal interface {
	// update trata a mensagem; devolve um comando e se a janela deve fechar.
	update(m *Model, msg tea.Msg) (tea.Cmd, bool)
	view(m *Model) string
}

// ---- peças comuns ----

func newInput(placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.Width = width
	ti.CharLimit = 256
	ti.TextStyle = sFg
	ti.PlaceholderStyle = sFaint
	ti.Cursor.Style = sAcc
	return ti
}

func dialog(title string, width int, body string) string {
	inner := width - 2
	closeBtn := zone.Mark("m:close", " × ")
	bar := lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).
		Width(inner - lipgloss.Width(closeBtn)).Render(" " + title)
	bar += lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Render(closeBtn)
	content := lipgloss.NewStyle().Padding(1, 2, 0, 2).Width(inner).Render(body)
	return lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(cAcc).
		Render(bar + "\n" + content)
}

func fieldRow(label string, focused bool, value string) string {
	l := lipgloss.NewStyle().Width(11).Foreground(cDim)
	mark := "  "
	if focused {
		l = l.Foreground(cAcc).Bold(true)
		mark = sAcc.Render("› ")
	}
	// JoinHorizontal mantém valores de várias linhas alinhados à coluna
	return lipgloss.JoinHorizontal(lipgloss.Top, mark, l.Render(label), value)
}

func button(id, label string, main, focused bool) string {
	st := sBtnGhost
	if main {
		st = sBtnMain
	}
	if focused {
		st = st.Underline(true)
		label = "›" + label[1:len(label)-1] + "‹"
	}
	return zone.Mark(id, st.Render(label))
}

func clicked(id string, msg tea.MouseMsg) bool { return zone.Get(id).InBounds(msg) }

// isLeftClick filtra só o apertar do botão esquerdo
// (no formato antigo de mouse, o soltar não diz qual botão foi).
func isLeftClick(msg tea.Msg) (tea.MouseMsg, bool) {
	mm, ok := msg.(tea.MouseMsg)
	return mm, ok && mm.Button == tea.MouseButtonLeft && mm.Action == tea.MouseActionPress
}

// cycle anda pela lista de opções.
func cycle(list []string, cur string, d int) string {
	i := 0
	for j, x := range list {
		if x == cur {
			i = j
		}
	}
	return list[(i+d+len(list))%len(list)]
}

func folderLabel(p string) string {
	if p == "" {
		return "Sem pasta"
	}
	return strings.ReplaceAll(p, "/", " › ")
}

// ---- confirmar ----

type confirmModal struct {
	title, text, okLabel string
	onOK                 func(m *Model) tea.Cmd
	focusOK              bool
}

func (c *confirmModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:ok", mm):
			return c.onOK(m), true
		case clicked("m:cancel", mm), clicked("m:close", mm):
			return nil, true
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "esc", "n":
		return nil, true
	case "s", "y":
		return c.onOK(m), true
	case "tab", "shift+tab", "left", "right", "h", "l":
		c.focusOK = !c.focusOK
	case "enter":
		if c.focusOK {
			return c.onOK(m), true
		}
		return nil, true
	}
	return nil, false
}

func (c *confirmModal) view(m *Model) string {
	btns := button("m:cancel", " Cancelar ", false, !c.focusOK) + "  " +
		lipgloss.NewStyle().Render(button("m:ok", " "+c.okLabel+" ", true, c.focusOK))
	body := sFg.Render(c.text) + "\n\n" + lipgloss.PlaceHorizontal(44, lipgloss.Right, btns) + "\n"
	return dialog(c.title, 50, body)
}
