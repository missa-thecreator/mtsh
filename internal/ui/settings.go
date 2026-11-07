package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/fonts"
	"mtsh/internal/update"
)

// ---- atualização ----

type updateAvailableMsg struct{ tag string }

type updateAppliedMsg struct {
	tag string
	out string
	err error
}

// checkUpdate consulta em segundo plano se saiu versão nova (no máximo 1x por dia).
func checkUpdate(version string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tag, err := update.Check(ctx, version)
		if err != nil || tag == "" {
			return nil
		}
		return updateAvailableMsg{tag}
	}
}

func applyUpdate(version string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var buf bytes.Buffer
		tag, err := update.Apply(ctx, version, &buf)
		return updateAppliedMsg{tag, buf.String(), err}
	}
}

func (m *Model) onUpdateAvailable(tag string) {
	m.newerTag = tag
	if m.toast == "" {
		m.say("versão "+tag+" disponível · aperte , pra atualizar", false)
	}
}

func (m *Model) onUpdateApplied(msg updateAppliedMsg) {
	if s, ok := m.modal.(*settingsModal); ok {
		s.updating = false
		s.updOut, s.updErr = strings.TrimSpace(msg.out), msg.err
	}
	if msg.err == nil && msg.tag != "" {
		m.newerTag, m.updatedTo = "", msg.tag
		m.say("atualizado pra "+msg.tag+" · feche e abra o mtsh pra usar", false)
	}
}

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
	focus      int // 0 atualizar, 1 instalar ícones, 2 aparência, 3 fechar
	installing bool
	output     string
	err        error

	updating bool
	updOut   string
	updErr   error
}

func (s *settingsModal) runUpdate(m *Model) tea.Cmd {
	if s.updating || m.newerTag == "" {
		return nil
	}
	s.updating, s.updOut, s.updErr = true, "", nil
	return applyUpdate(m.version)
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
		case clicked("s:update", mm):
			s.focus = 0
			return s.runUpdate(m), false
		case clicked("s:install", mm):
			s.focus = 1
			return s.install(), false
		case clicked("s:logo", mm):
			s.focus = 2
			s.setSimple(m, false)
		case clicked("s:dot", mm):
			s.focus = 2
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
		s.focus = (s.focus + 1) % 4
	case "shift+tab", "up", "k":
		s.focus = (s.focus + 3) % 4
	case "left", "right", "h", "l", " ":
		if s.focus == 2 {
			s.setSimple(m, !m.cfg.SimpleIcons)
		}
	case "enter":
		switch s.focus {
		case 0:
			return s.runUpdate(m), false
		case 1:
			return s.install(), false
		case 2:
			s.setSimple(m, !m.cfg.SimpleIcons)
		case 3:
			return nil, true
		}
	}
	return nil, false
}

func (s *settingsModal) view(m *Model) string {
	head := lipgloss.NewStyle().Foreground(cFg).Bold(true)
	var rows []string

	// versão
	rows = append(rows, head.Render("Versão"), "")
	switch {
	case m.updatedTo != "":
		rows = append(rows, sFg.Render("  mtsh "+m.version+" → ")+sOk.Render(m.updatedTo+" instalada")+sDim.Render(" · feche e abra o mtsh"))
	case m.newerTag != "":
		label := " Atualizar pra " + m.newerTag + " "
		if s.updating {
			label = " Atualizando… "
		}
		rows = append(rows, sFg.Render("  mtsh "+m.version+"  ")+sAcc.Render(m.newerTag+" disponível"), "",
			"  "+button("s:update", label, true, s.focus == 0))
	case errors.Is(s.updErr, update.ErrDev) || !strings.HasPrefix(m.version, "v"):
		rows = append(rows, sDim.Render("  mtsh "+m.version+" (desenvolvimento)"))
	default:
		rows = append(rows, sFg.Render("  mtsh "+m.version+"  ")+sOk.Render("✓ última versão"))
	}
	if s.updErr != nil {
		rows = append(rows, sErr.Width(60).Render("  ✗ "+s.updErr.Error()))
	}
	for _, l := range strings.Split(s.updOut, "\n") {
		if l != "" {
			rows = append(rows, wrapDim(l))
		}
	}

	rows = append(rows, "", head.Render("Ícones das distros"), "")
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
	rows = append(rows, "  "+button("s:install", label, true, s.focus == 1))
	if s.err != nil {
		rows = append(rows, "", sErr.Width(60).Render("  ✗ "+s.err.Error()))
	}
	if s.output != "" {
		rows = append(rows, "")
		for _, l := range strings.Split(s.output, "\n") {
			rows = append(rows, wrapDim(l))
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
		if s.focus == 2 && on {
			st = sAcc
		}
		return zone.Mark(id, st.Render(mark+" "+label))
	}
	rows = append(rows, "", head.Render("Como os servidores aparecem"), "",
		"  "+radio("s:logo", !m.cfg.SimpleIcons, "Logo da distro")+"    "+radio("s:dot", m.cfg.SimpleIcons, "Bolinha na cor da distro"),
		"", sFaint.Render("  tab próximo · enter escolher · esc fechar"),
		lipgloss.PlaceHorizontal(60, lipgloss.Right, button("m:cancel", " Fechar ", false, s.focus == 3)), "")
	return dialog("Ajustes", 66, strings.Join(rows, "\n"))
}

// wrapDim quebra linhas longas (ex: caminhos) dentro da janela de Ajustes.
func wrapDim(l string) string {
	return lipgloss.NewStyle().Foreground(cDim).Width(60).PaddingLeft(2).Render(l)
}
