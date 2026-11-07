package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"

	"mtsh/internal/config"
)

const (
	btnConnect = " Conectar "
	btnEdit    = " Editar "
)

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	var body string
	if m.modal != nil {
		// janela no meio da área principal (ou da tela toda, se não couber)
		dlg := m.modal.view(&m)
		if lipgloss.Width(dlg) <= m.mainW() {
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.viewSide(),
				lipgloss.Place(m.mainW()+2, m.bodyH(), lipgloss.Center, lipgloss.Center, dlg))
		} else {
			body = lipgloss.Place(m.width, m.bodyH(), lipgloss.Center, lipgloss.Center, dlg)
		}
	} else {
		main := m.viewMain()
		if m.current() != nil {
			main = m.viewSession()
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.viewSide(), main)
	}
	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, m.viewTabs(), body, m.viewStatus()))
}

// ---- barra de abas ----

func (m Model) viewTabs() string {
	left := sLogo.Render("mtsh") + m.viewTabLabels()
	right := zone.Mark("act:settings", sBtnGhost.Foreground(cDim).Render(" Ajustes ")) + " "
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---- barra lateral ----

func (m Model) viewSide() string {
	inner := sideW - 1
	focused := m.focus == focusSide
	var b strings.Builder

	b.WriteString(sSideTitle.Render(" SERVIDORES") + "\n")

	var search string
	switch {
	case m.searching:
		search = sFg.Render(" ⌕ "+m.query) + sAcc.Render("▏")
	case m.query != "":
		search = sAcc.Render(" ⌕ " + m.query)
	default:
		search = sFaint.Render(" ⌕ buscar            /")
	}
	b.WriteString(zone.Mark("act:search", search) + "\n\n")

	rows := m.treeRows()
	end := min(len(rows), m.sideOff+m.treeVisible())
	for i := m.sideOff; i < end; i++ {
		b.WriteString(zone.Mark(fmt.Sprintf("tree:%d", i), m.treeLine(rows[i], i, inner, focused)) + "\n")
	}
	for i := end - m.sideOff; i < m.treeVisible(); i++ {
		b.WriteString("\n")
	}
	b.WriteString(" " + zone.Mark("act:folder", sBtnGhost.Render(" + Pasta ")))

	border := cLine
	if focused {
		border = cAcc
	}
	return lipgloss.NewStyle().
		Width(inner).Height(m.bodyH()).MaxHeight(m.bodyH()).
		BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(border).
		Render(b.String())
}

func (m Model) treeLine(r treeRow, i, w int, focused bool) string {
	indent := strings.Repeat("  ", r.depth)
	var text, count string
	isCtx := m.query == ""
	switch r.kind {
	case rowAll:
		text, count = "≡ Todos", fmt.Sprint(len(m.cfg.Hosts))
		isCtx = isCtx && m.ctx == ""
	case rowFolder:
		arrow := "▸"
		if m.open[r.path] {
			arrow = "▾"
		}
		name := config.Base(r.path)
		if ic := m.cfg.Style(r.path).Icon; ic != "" {
			name = ic + " " + name
		}
		text, count = indent+arrow+" "+name, fmt.Sprint(m.cfg.Count(r.path))
		isCtx = isCtx && m.ctx == r.path
	case rowHost:
		if r.depth == 0 {
			text = m.markChar(r.host) + " " + r.host.Name // sem pasta: alinhado com as pastas da raiz
		} else {
			text = indent + "  " + m.markChar(r.host) + " " + r.host.Name
		}
		isCtx = false
	}
	text = runewidth.Truncate(" "+text, w-len(count)-2, "…")
	line := runewidth.FillRight(text, w-len(count)-1) + count + " "

	switch {
	case i == m.sideSel && focused:
		return sRowSelFocus.Render(line)
	case i == m.sideSel:
		return sRowSel.Render(line)
	case r.kind == rowFolder:
		// pasta na cor escolhida (negrito se for a pasta aberta à direita)
		st := lipgloss.NewStyle().Foreground(folderColor(m.cfg.Style(r.path).Color)).Bold(isCtx)
		return st.Render(runewidth.FillRight(text, w-len(count)-1)) + sFaint.Render(count+" ")
	case isCtx:
		return sRowCtx.Render(line)
	case r.kind == rowHost:
		// símbolo da distro colorido, resto apagado
		mark := m.markChar(r.host)
		before, after, _ := strings.Cut(line, mark)
		return sDim.Render(before) + m.hostMark(r.host) + sDim.Render(after)
	}
	return sFg.Render(runewidth.FillRight(text, w-len(count)-1)) + sFaint.Render(count+" ")
}

// ---- área principal ----

func (m Model) viewMain() string {
	w := m.mainW()
	cards := m.cards()

	var title string
	switch {
	case m.query != "":
		title = sBold.Render("Busca: ") + sAcc.Render(m.query)
	case m.ctx == "":
		title = sBold.Render("Todos")
	default:
		if p := config.Parent(m.ctx); p != "" {
			title = sDim.Render(strings.ReplaceAll(p, "/", " › ") + " › ")
		}
		st := m.cfg.Style(m.ctx)
		title += folderTitle(st.Icon, st.Color, config.Base(m.ctx))
	}
	title += sDim.Render(fmt.Sprintf("   %d servidores", len(cards)))
	btn := zone.Mark("act:new", sBtnMain.Render(" + Servidor "))
	if m.ctx != "" && m.query == "" {
		btn = zone.Mark("act:editfolder", sBtnGhost.Render(" Editar pasta ")) + " " + btn
	}
	gap := max(1, w-lipgloss.Width(title)-lipgloss.Width(btn))
	header := title + strings.Repeat(" ", gap) + btn

	var grid string
	if len(cards) == 0 {
		grid = m.emptyState(w)
	} else {
		grid = m.viewGrid(cards)
	}
	return lipgloss.NewStyle().PaddingLeft(2).Width(w + 2).Height(m.bodyH()).MaxHeight(m.bodyH()).
		Render(header + "\n\n" + grid)
}

func (m Model) emptyState(w int) string {
	if m.query != "" {
		return sDim.Render("Nada encontrado pra \"" + m.query + "\".")
	}
	if len(m.cfg.Hosts) == 0 {
		return lipgloss.NewStyle().Width(min(w, 70)).Render(
			sBold.Render("Nenhum servidor ainda.") + "\n\n" +
				sDim.Render("O cadastro pela tela chega no próximo passo. Por enquanto, adicione no arquivo:") + "\n" +
				sAcc.Render(m.cfgPath) + "\n\n" +
				sFaint.Render("hosts:\n  - name: meu-servidor\n    folder: Casa\n    host: 192.168.0.10\n    user: root"))
	}
	return sDim.Render("Essa pasta está vazia.")
}

func (m Model) viewGrid(cards []config.Host) string {
	cols, cw := m.cols(), m.cardW()
	var lines []string
	first := m.gridOff * cols
	last := min(len(cards), first+m.gridRows()*cols)
	for start := first; start < last; start += cols {
		var row []string
		for i := start; i < min(start+cols, last); i++ {
			if i > start {
				row = append(row, " ")
			}
			row = append(row, zone.Mark(fmt.Sprintf("card:%d", i), m.viewCard(cards[i], cw, i == m.cardSel)))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewCard(h config.Host, w int, sel bool) string {
	inner := w - 4
	focused := sel && m.focus == focusGrid

	nameStyle := sBold
	if focused {
		nameStyle = nameStyle.Foreground(cAcc)
	}
	name := m.hostMark(h) + " " + nameStyle.Render(runewidth.Truncate(h.Name, inner-2, "…"))
	if m.query != "" && h.Folder != "" {
		right := sFaint.Render(runewidth.Truncate(config.Base(h.Folder), inner/2, "…"))
		if gap := inner - lipgloss.Width(name) - lipgloss.Width(right); gap > 0 {
			name += strings.Repeat(" ", gap) + right
		}
	}

	who := h.Host
	if h.User != "" {
		who = h.User + " · " + h.Host
	}
	if h.Port != 0 && h.Port != 22 {
		who += fmt.Sprintf(":%d", h.Port)
	}

	// linha do sistema + tags, ex: "Ubuntu 24.04 · k8s"
	tags := osLabel(h)
	if len(h.Tags) > 0 {
		t := strings.Join(h.Tags, "  ")
		if tags != "" {
			t = runewidth.Truncate(t, max(0, inner-lipgloss.Width(tags)-3), "…")
			tags += sFaint.Render(" · ")
		}
		tags += sOk.Render(runewidth.Truncate(t, inner, "…"))
	}
	if tags == "" {
		tags = sFaint.Render("sistema aparece ao conectar")
	}

	connect := sBtnGhost.Render(btnConnect)
	if sel {
		connect = sBtnMain.Render(btnConnect)
	}
	buttons := connect + " " + sBtnGhost.Foreground(cDim).Render(btnEdit)

	border := cLine
	if focused {
		border = cAcc
	} else if sel {
		border = cDim
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2).
		Render(strings.Join([]string{name, sDim.Render(runewidth.Truncate(who, inner, "…")), tags, buttons}, "\n"))
}

// ---- barra de status ----

func (m Model) viewStatus() string {
	left := sDim.Render(fmt.Sprintf(" %d servidores · %d pastas", len(m.cfg.Hosts), len(m.cfg.Folders)))
	if s := m.current(); s != nil {
		left = sDim.Render(" ") + sOk.Render("● ") + sFg.Render(s.Name)
		if h, ok := m.hostByName(s.Name); ok {
			left += sDim.Render(" · " + h.Target())
		}
	}
	if m.toast != "" {
		st := sOk
		if m.toastErr {
			st = sErr
		}
		left += "   " + st.Render(m.toast)
	}

	var keys string
	switch {
	case m.current() != nil:
		keys = "ctrl+b volta pro Início · clique nas abas pra trocar"
	case m.modal != nil:
		keys = "tab próximo · enter confirmar · esc fechar"
	case m.searching:
		keys = "digite pra buscar · enter ok · esc limpar"
	case m.focus == focusSide:
		keys = "enter abrir · e editar pasta · a servidor · n pasta · d apagar · / buscar · q sair"
	default:
		keys = "enter conectar · t tela cheia · a novo · e editar · d apagar · n pasta · / buscar · , ajustes · q sair"
	}
	right := sFaint.Render(keys + " ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return runewidth.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}
