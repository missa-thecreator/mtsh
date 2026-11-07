package ui

import (
	"bytes"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/fonts"
)

type iconStatusMsg struct{ st fonts.Status }

type iconInstallMsg struct {
	out string
	err error
}

// checkIcons confere em segundo plano se os logos das distros vão aparecer.
func checkIcons() tea.Msg { return iconStatusMsg{fonts.Check()} }

func installIcons() tea.Msg {
	var buf bytes.Buffer
	err := fonts.Install(&buf)
	return iconInstallMsg{buf.String(), err}
}

func (m *Model) onIconStatus(st fonts.Status) {
	first := m.iconStatus == nil
	m.iconStatus = &st
	if first && !st.Ready() && st.Err == nil && m.toast == "" {
		m.say("os logos das distros precisam de uma fonte · aperte , pra configurar", false)
	}
}

func (m *Model) onIconInstall(msg iconInstallMsg) tea.Cmd {
	if s, ok := m.modal.(*settingsModal); ok {
		s.installing = false
		s.output, s.err = strings.TrimSpace(msg.out), msg.err
	}
	return checkIcons
}

// ---- janela de Ajustes ----

type settingsModal struct {
	focus      int // 0 instalar, 1 aparência, 2 fechar
	installing bool
	output     string
	err        error
}

func (s *settingsModal) setSimple(m *Model, simple bool) {
	if m.cfg.SimpleIcons != simple {
		m.cfg.SimpleIcons = simple
		m.persist()
	}
}

func (s *settingsModal) install() tea.Cmd {
	if s.installing {
		return nil
	}
	s.installing, s.output, s.err = true, "", nil
	return installIcons
}

func (s *settingsModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:close", mm), clicked("m:cancel", mm):
			return nil, true
		case clicked("s:install", mm):
			s.focus = 0
			return s.install(), false
		case clicked("s:logo", mm):
			s.focus = 1
			s.setSimple(m, false)
		case clicked("s:dot", mm):
			s.focus = 1
			s.setSimple(m, true)
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "esc", "q", ",":
		return nil, true
	case "tab", "down", "j":
		s.focus = (s.focus + 1) % 3
	case "shift+tab", "up", "k":
		s.focus = (s.focus + 2) % 3
	case "left", "right", "h", "l", " ":
		if s.focus == 1 {
			s.setSimple(m, !m.cfg.SimpleIcons)
		}
	case "enter":
		switch s.focus {
		case 0:
			return s.install(), false
		case 1:
			s.setSimple(m, !m.cfg.SimpleIcons)
		case 2:
			return nil, true
		}
	}
	return nil, false
}

func (s *settingsModal) view(m *Model) string {
	head := lipgloss.NewStyle().Foreground(cFg).Bold(true)
	var rows []string

	rows = append(rows, head.Render("Ícones das distros"), "")
	st := m.iconStatus
	switch {
	case st == nil:
		rows = append(rows, sDim.Render("  verificando…"))
	case st.Err != nil:
		rows = append(rows, sErr.Render("  ✗ não deu pra verificar: "+st.Err.Error()))
	default:
		if st.FontInstalled {
			rows = append(rows, sOk.Render("  ✓ ")+sFg.Render("Fonte dos logos instalada"))
		} else {
			rows = append(rows, sErr.Render("  ✗ ")+sFg.Render("Fonte dos logos não instalada"))
		}
		for _, t := range st.Terminals {
			if t.OK {
				rows = append(rows, sOk.Render("  ✓ ")+sFg.Render(t.Name+" configurado"))
			} else {
				rows = append(rows, sAcc.Render("  ! ")+sFg.Render(t.Name+": ")+sDim.Render(t.Note))
			}
		}
	}

	// linha de teste: se a fonte estiver funcionando, aparecem os logos
	var sample []string
	for _, id := range []string{"ubuntu", "debian", "linuxmint", "fedora", "rhel", "rocky", "arch", "alpine"} {
		d := distros[id]
		sample = append(sample, lipgloss.NewStyle().Foreground(d.color).Render(d.glyph))
	}
	rows = append(rows, "", "  "+sDim.Render("Teste: ")+strings.Join(sample, " ")+sFaint.Render("   ← vê os logos? tudo certo"), "")

	label := " Instalar ícones "
	if st != nil && st.Ready() {
		label = " Reinstalar ícones "
	}
	if s.installing {
		label = " Instalando… "
	}
	rows = append(rows, "  "+button("s:install", label, true, s.focus == 0))
	if s.err != nil {
		rows = append(rows, "", sErr.Render("  ✗ "+s.err.Error()))
	}
	if s.output != "" {
		rows = append(rows, "")
		for _, l := range strings.Split(s.output, "\n") {
			rows = append(rows, sDim.Render("  "+l))
		}
	}

	// aparência
	radio := func(id string, on bool, label string) string {
		mark := "( )"
		st := sDim
		if on {
			mark = "(•)"
			st = sFg
		}
		if s.focus == 1 && on {
			st = sAcc
		}
		return zone.Mark(id, st.Render(mark+" "+label))
	}
	rows = append(rows, "", head.Render("Como os servidores aparecem"), "",
		"  "+radio("s:logo", !m.cfg.SimpleIcons, "Logo da distro")+"    "+radio("s:dot", m.cfg.SimpleIcons, "Bolinha na cor da distro"),
		"", sFaint.Render("  tab próximo · enter escolher · esc fechar"),
		lipgloss.PlaceHorizontal(60, lipgloss.Right, button("m:cancel", " Fechar ", false, s.focus == 2)), "")
	return dialog("Ajustes", 66, strings.Join(rows, "\n"))
}
