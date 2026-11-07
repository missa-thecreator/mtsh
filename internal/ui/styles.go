package ui

import "github.com/charmbracelet/lipgloss"

// Paleta igual à da preview.
var (
	cFg    = lipgloss.Color("#D4DCD7")
	cDim   = lipgloss.Color("#7A8881")
	cFaint = lipgloss.Color("#44514B")
	cLine  = lipgloss.Color("#2C3531")
	cSel   = lipgloss.Color("#232B27")
	cAcc   = lipgloss.Color("#F0B429")
	cOk    = lipgloss.Color("#4FD1B5")
	cErr   = lipgloss.Color("#FF6B6B")
	cDark  = lipgloss.Color("#121614")
)

var (
	sFg    = lipgloss.NewStyle().Foreground(cFg)
	sDim   = lipgloss.NewStyle().Foreground(cDim)
	sFaint = lipgloss.NewStyle().Foreground(cFaint)
	sAcc   = lipgloss.NewStyle().Foreground(cAcc)
	sOk    = lipgloss.NewStyle().Foreground(cOk)
	sErr   = lipgloss.NewStyle().Foreground(cErr)
	sBold  = lipgloss.NewStyle().Foreground(cFg).Bold(true)

	sLogo      = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Padding(0, 1)
	sTabOn     = lipgloss.NewStyle().Background(cSel).Foreground(cFg).Padding(0, 2)
	sBtnMain   = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
	sBtnGhost  = lipgloss.NewStyle().Background(cLine).Foreground(cFg)
	sSideTitle = lipgloss.NewStyle().Foreground(cDim).Bold(true)

	sRowSel      = lipgloss.NewStyle().Background(cSel).Foreground(cFg)
	sRowSelFocus = lipgloss.NewStyle().Background(cSel).Foreground(cAcc).Bold(true)
	sRowCtx      = lipgloss.NewStyle().Foreground(cFg).Bold(true)
)

// ---- aparência das pastas ----

// folderIcons são os ícones que dá pra escolher ("" = sem ícone).
var folderIcons = []string{"", "📁", "🏠", "🏢", "💼", "🧪", "🐳", "🚀", "🌐", "💾", "📦", "🔒", "🔧", "🔥", "⭐", "🎮"}

// folderColors é a paleta das pastas; o id é o que vai pro hosts.yaml.
var folderColors = []struct {
	id, label string
	color     lipgloss.Color
}{
	{"", "padrão", cFg},
	{"amber", "âmbar", lipgloss.Color("#F0B429")},
	{"teal", "verde-água", lipgloss.Color("#4FD1B5")},
	{"green", "verde", lipgloss.Color("#7BD88F")},
	{"blue", "azul", lipgloss.Color("#5B9CF6")},
	{"purple", "roxo", lipgloss.Color("#B48EF7")},
	{"pink", "rosa", lipgloss.Color("#F27BB4")},
	{"red", "vermelho", lipgloss.Color("#FF6B6B")},
	{"gray", "cinza", lipgloss.Color("#7A8881")},
}

func folderColor(id string) lipgloss.Color {
	for _, c := range folderColors {
		if c.id == id {
			return c.color
		}
	}
	return cFg
}
