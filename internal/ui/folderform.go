package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/config"
)

// folderModal é a janela de nova pasta / editar pasta.
type folderModal struct {
	orig   string // caminho original ("" = pasta nova)
	name   textinput.Model
	parent string
	icon   string
	color  string
	focus  int // 0 nome, 1 dentro de, 2 ícone, 3 cor, 4 cancelar, 5 salvar
	err    string
}

const iconsPerRow = 8

func newFolderModal(parent string) *folderModal {
	f := &folderModal{name: newInput("Clientes", 30), parent: parent, icon: "📁"}
	f.setFocus(0)
	return f
}

func editFolderModal(m *Model, path string) *folderModal {
	st := m.cfg.Style(path)
	f := &folderModal{orig: path, name: newInput("", 30), parent: config.Parent(path), icon: st.Icon, color: st.Color}
	f.name.SetValue(config.Base(path))
	f.setFocus(0)
	return f
}

func (f *folderModal) setFocus(i int) {
	f.focus = (i%6 + 6) % 6
	if f.focus == 0 {
		f.name.Focus()
	} else {
		f.name.Blur()
	}
}

// parents são as pastas onde esta pode ficar (não dá pra mover pra dentro dela mesma).
func (f *folderModal) parents(m *Model) []string {
	out := []string{""}
	for _, p := range m.cfg.Folders {
		if f.orig == "" || (p != f.orig && !strings.HasPrefix(p, f.orig+"/")) {
			out = append(out, p)
		}
	}
	return out
}

func colorIDs() []string {
	ids := make([]string, len(folderColors))
	for i, c := range folderColors {
		ids[i] = c.id
	}
	return ids
}

func (f *folderModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:close", mm), clicked("m:cancel", mm):
			return nil, true
		case clicked("m:ok", mm):
			return nil, f.submit(m)
		case clicked("f:name", mm):
			f.setFocus(0)
		case clicked("f:parent", mm):
			f.setFocus(1)
			f.parent = cycle(f.parents(m), f.parent, 1)
		}
		for i, ic := range folderIcons {
			if clicked(fmt.Sprintf("f:icon:%d", i), mm) {
				f.icon = ic
				f.setFocus(2)
			}
		}
		for i, c := range folderColors {
			if clicked(fmt.Sprintf("f:color:%d", i), mm) {
				f.color = c.id
				f.setFocus(3)
			}
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if f.focus == 0 {
			var cmd tea.Cmd
			f.name, cmd = f.name.Update(msg)
			return cmd, false
		}
		return nil, false
	}
	f.err = ""
	switch k.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		if f.focus == 2 && k.String() == "down" && indexOf(folderIcons, f.icon) < iconsPerRow {
			f.icon = folderIcons[min(len(folderIcons)-1, indexOf(folderIcons, f.icon)+iconsPerRow)]
			return nil, false
		}
		f.setFocus(f.focus + 1)
		return nil, false
	case "shift+tab", "up":
		if f.focus == 2 && k.String() == "up" && indexOf(folderIcons, f.icon) >= iconsPerRow {
			f.icon = folderIcons[indexOf(folderIcons, f.icon)-iconsPerRow]
			return nil, false
		}
		f.setFocus(f.focus - 1)
		return nil, false
	case "enter":
		if f.focus == 4 {
			return nil, true
		}
		return nil, f.submit(m)
	case "left", "right":
		d := 1
		if k.String() == "left" {
			d = -1
		}
		switch f.focus {
		case 1:
			f.parent = cycle(f.parents(m), f.parent, d)
			return nil, false
		case 2:
			f.icon = cycle(folderIcons, f.icon, d)
			return nil, false
		case 3:
			f.color = cycle(colorIDs(), f.color, d)
			return nil, false
		case 4, 5:
			f.setFocus(9 - f.focus)
			return nil, false
		}
	}
	if f.focus == 0 {
		var cmd tea.Cmd
		f.name, cmd = f.name.Update(msg)
		return cmd, false
	}
	return nil, false
}

func indexOf(list []string, x string) int {
	for i, v := range list {
		if v == x {
			return i
		}
	}
	return 0
}

func (f *folderModal) submit(m *Model) bool {
	name := strings.TrimSpace(strings.ReplaceAll(f.name.Value(), "/", "-"))
	if name == "" {
		f.err = "Dá um nome pra pasta."
		f.setFocus(0)
		return false
	}
	path := name
	if f.parent != "" {
		path = f.parent + "/" + name
	}
	if path != f.orig {
		for _, x := range m.cfg.Folders {
			if x == path {
				f.err = "Já existe uma pasta com esse nome aí."
				f.setFocus(0)
				return false
			}
		}
	}

	if f.orig == "" {
		m.cfg.Folders = append(m.cfg.Folders, path)
	} else if path != f.orig {
		m.cfg.RenameFolder(f.orig, path)
		// pastas abertas na árvore acompanham o novo caminho
		open := map[string]bool{}
		for p, v := range m.open {
			if p == f.orig || strings.HasPrefix(p, f.orig+"/") {
				p = path + strings.TrimPrefix(p, f.orig)
			}
			open[p] = v
		}
		m.open = open
	}
	m.cfg.SetStyle(path, config.FolderStyle{Icon: f.icon, Color: f.color})
	if !m.persist() {
		return true
	}
	for p := path; p != ""; p = config.Parent(p) {
		m.open[p] = true
	}
	m.query, m.ctx, m.cardSel = "", path, 0
	m.selectTreeFolder(path)
	if f.orig == "" {
		m.say("pasta "+name+" criada", false)
	} else {
		m.say("pasta "+name+" salva", false)
	}
	return true
}

func (f *folderModal) view(m *Model) string {
	parent := zone.Mark("f:parent", sFg.Render(folderLabel(f.parent))+sFaint.Render("  ◂ ▸"))

	// ícones em duas linhas
	var iconRows []string
	var row []string
	for i, ic := range folderIcons {
		label := ic
		if ic == "" {
			label = "--"
		}
		cell := " " + label + " "
		if ic == f.icon {
			st := lipgloss.NewStyle().Background(cLine)
			if f.focus == 2 {
				st = lipgloss.NewStyle().Background(cAcc).Foreground(cDark)
			}
			cell = st.Render(cell)
		} else if ic == "" {
			cell = sFaint.Render(cell)
		}
		row = append(row, zone.Mark(fmt.Sprintf("f:icon:%d", i), cell))
		if len(row) == iconsPerRow || i == len(folderIcons)-1 {
			iconRows = append(iconRows, strings.Join(row, ""))
			row = nil
		}
	}

	// cores
	var swatches []string
	colorName := ""
	for i, c := range folderColors {
		sw := lipgloss.NewStyle().Foreground(c.color).Render("██")
		if c.id == f.color {
			colorName = c.label
			br := sDim
			if f.focus == 3 {
				br = sAcc
			}
			sw = br.Render("[") + sw + br.Render("]")
		} else {
			sw = " " + sw + " "
		}
		swatches = append(swatches, zone.Mark(fmt.Sprintf("f:color:%d", i), sw))
	}

	name := f.name.Value()
	if name == "" {
		name = "Clientes"
	}
	preview := folderTitle(f.icon, f.color, name)

	rows := []string{
		zone.Mark("f:name", fieldRow("Nome", f.focus == 0, f.name.View())),
		fieldRow("Dentro de", f.focus == 1, parent),
		"",
		fieldRow("Ícone", f.focus == 2, strings.Join(iconRows, "\n")),
		"",
		fieldRow("Cor", f.focus == 3, strings.Join(swatches, "")+"  "+sDim.Render(colorName)),
		"",
		fieldRow("Prévia", false, sFaint.Render("▾ ")+preview),
		"",
	}
	if f.err != "" {
		rows = append(rows, sErr.Render("  "+f.err))
	} else {
		rows = append(rows, sFaint.Render("  tab próximo · ◂ ▸ escolher · enter salvar · esc fechar"))
	}
	okLabel := " Criar pasta "
	title := "Nova pasta"
	if f.orig != "" {
		okLabel = " Salvar "
		title = "Editar pasta " + config.Base(f.orig)
	}
	btns := button("m:cancel", " Cancelar ", false, f.focus == 4) + "  " + button("m:ok", okLabel, true, f.focus == 5)
	rows = append(rows, lipgloss.PlaceHorizontal(56, lipgloss.Right, btns), "")
	return dialog(title, 62, strings.Join(rows, "\n"))
}

// folderTitle desenha "ícone nome" na cor da pasta.
func folderTitle(icon, color, name string) string {
	st := lipgloss.NewStyle().Foreground(folderColor(color)).Bold(true)
	if icon != "" {
		return icon + " " + st.Render(name)
	}
	return st.Render(name)
}
