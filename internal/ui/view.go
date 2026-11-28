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
	// cada região com o seu fundo: painel (abas, lateral, rodapé) e área principal
	side := paint(m.viewSide(), m.sideW(), cPanel)
	mainW := m.width - m.sideW()
	var body string
	if m.modal != nil {
		// janela no meio da área principal (ou da tela toda, se não couber)
		dlg := m.modal.view(&m)
		if lipgloss.Width(dlg) <= m.mainW() {
			body = lipgloss.JoinHorizontal(lipgloss.Top, side,
				paint(lipgloss.Place(mainW, m.bodyH(), lipgloss.Center, lipgloss.Center, dlg), mainW, cBg))
		} else {
			body = paint(lipgloss.Place(m.width, m.bodyH(), lipgloss.Center, lipgloss.Center, dlg), m.width, cBg)
		}
	} else {
		main := m.viewMain()
		if m.current() != nil {
			main = m.viewSession()
		} else if v := m.currentDB(); v != nil {
			main = m.viewDBTab(v)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, side, paint(main, mainW, cBg))
	}
	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left,
		paint(m.viewTabs(), m.width, cPanel), body, paint(m.viewStatus(), m.width, cPanel)))
}

// ---- barra de abas ----

func (m Model) viewTabs() string {
	left := sLogo.Render("mtsh") + " " + m.viewTabLabels()
	right := zone.Mark("act:settings", sBtnGhost.Foreground(cDim).Render(" Ajustes ")) + " "
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---- barra lateral ----

func (m Model) viewSide() string {
	inner := m.sideW() - 1
	focused := m.focus == focusSide
	var b strings.Builder

	b.WriteString(sSideTitle.Render(" SERVIDORES") + "\n")

	// busca com cara de campo: caixa rebaixada, "/" na ponta como dica
	fw := inner - 4 // margem de 1 de cada lado + espaço interno do campo
	var content string
	switch {
	case m.searching:
		content = sAcc.Render(m.searchIcon()) + sFg.Render(" "+m.query) + sAcc.Render("▏")
	case m.query != "":
		content = sAcc.Render(m.searchIcon() + " " + m.query)
	default:
		hint := sFaint.Render("/")
		label := sFaint.Render(m.searchIcon() + " buscar")
		content = label + strings.Repeat(" ", max(1, fw-lipgloss.Width(label)-1)) + hint
	}
	b.WriteString(" " + zone.Mark("act:search", fieldBox(content, fw)) + "\n\n")

	rows := m.treeRows()
	end := min(len(rows), m.sideOff+m.treeVisible())
	for i := m.sideOff; i < end; i++ {
		b.WriteString(zone.Mark(fmt.Sprintf("tree:%d", i), m.treeLine(rows[i], i, inner, focused)) + "\n")
	}
	for i := end - m.sideOff; i < m.treeVisible(); i++ {
		b.WriteString("\n")
	}
	b.WriteString(" " + zone.Mark("act:folder", sBtnGhost.Render(" + Pasta ")))

	var border lipgloss.TerminalColor = cLine
	if focused || m.resizing {
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
	case rowImport:
		text = "⇩ Importar"
		isCtx = false
	case rowAll:
		text, count = "≡ Todos", fmt.Sprint(len(m.cfg.Hosts))
		isCtx = isCtx && m.ctx == "" && !m.dbMode
	case rowDBs:
		text, count = dbGlyph+" Bancos", fmt.Sprint(len(m.cfg.Databases))
		isCtx = isCtx && m.dbMode
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
		isCtx = isCtx && m.ctx == r.path && !m.dbMode
	case rowHost:
		if r.depth == 0 {
			text = m.markChar(r.host) + " " + r.host.Name // sem pasta: alinhado com as pastas da raiz
		} else {
			text = indent + "  " + m.markChar(r.host) + " " + r.host.Name
		}
		isCtx = false
	}
	// sessão aberta: bolinha verde no lugar da contagem
	if r.kind == rowHost && m.hasSession(r.host.Name) {
		count = "●"
	}
	cw := runewidth.StringWidth(count)
	avail := w - 2 - cw // marcador (1) + texto + contagem + espaço (1)
	body := runewidth.FillRight(runewidth.Truncate(text, avail, "…"), avail)

	inSession := m.tab != 0 // numa aba (servidor ou banco)
	active := inSession && r.kind == rowHost && r.host.Name == m.sessionHost()
	selected := !inSession && i == m.sideSel

	countStyle := func(st lipgloss.Style) string {
		if count == "●" {
			return st.Foreground(cOk).Render(count) + st.Render(" ")
		}
		return st.Render(count + " ")
	}
	bar := sAcc.Render("▌")
	switch {
	case active:
		// a sessão em que você está, sempre marcada durante a sessão
		st := lipgloss.NewStyle().Background(cSel).Foreground(cAcc).Bold(true)
		return bar + st.Render(body) + countStyle(lipgloss.NewStyle().Background(cSel))
	case selected && focused:
		return bar + sRowSelFocus.Render(body) + countStyle(sRowSelFocus)
	case selected:
		return sDim.Render("▌") + sRowSel.Render(body) + countStyle(sRowSel)
	case r.kind == rowFolder:
		// pasta na cor escolhida (negrito se for a pasta aberta à direita)
		st := lipgloss.NewStyle().Foreground(folderColor(m.cfg.Style(r.path).Color)).Bold(isCtx && !inSession)
		return " " + st.Render(body) + countStyle(sFaint)
	case isCtx && !inSession:
		return " " + sRowCtx.Render(body) + countStyle(sRowCtx)
	case r.kind == rowHost:
		// símbolo da distro colorido, resto apagado
		mark := m.markChar(r.host)
		before, after, _ := strings.Cut(body, mark)
		return " " + sDim.Render(before) + m.hostMark(r.host) + sDim.Render(after) + countStyle(sFaint)
	}
	return " " + sFg.Render(body) + countStyle(sFaint)
}

// ---- área principal ----

func (m Model) viewMain() string {
	if m.dbMode {
		return m.viewDBMain()
	}
	w := m.mainW()
	cards := m.cards()

	var title string
	switch {
	case strings.HasPrefix(m.query, "#") && len(m.query) > 1:
		tag := strings.ToLower(m.query[1:])
		title = sBold.Render("Tag: ") + tagStyle(tag).Bold(true).Render(tag) + sFaint.Render("  (esc limpa)")
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
				sDim.Render("Cadastre um em ") + sAcc.Render("+ Servidor") +
				sDim.Render(" ou traga os do MobaXterm ou do electerm em ") + sAcc.Render("⇩ Importar") + sDim.Render(", na barra lateral."))
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
	right := ""
	switch {
	case m.hasSession(h.Name):
		right = sOk.Render("● conectado")
	case m.query != "" && h.Folder != "":
		right = sFaint.Render(runewidth.Truncate(config.Base(h.Folder), inner/2, "…"))
	}
	if right != "" {
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

	// linha do sistema + tags, ex: "Ubuntu 24.04 · k8s"; clicar numa tag filtra
	tags, _ := cardTagsLine(h, inner)

	connect := sBtnGhost.Render(btnConnect)
	if sel {
		connect = sBtnMain.Render(btnConnect)
	}
	buttons := connect + " " + sBtnGhost.Foreground(cDim).Render(btnEdit)

	var border lipgloss.TerminalColor = cLine
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

// hint é um atalho do rodapé: a tecla (em destaque) e o que ela faz.
type hint struct{ key, desc string }

// statusHints são os atalhos de cada situação, do mais importante pro menos:
// quando falta espaço, os do fim saem primeiro.
func (m Model) statusHints() []hint {
	switch {
	case m.modal != nil:
		return []hint{{"enter", "confirmar"}, {"esc", "fechar"}, {"tab", "próximo campo"}}
	case m.currentDB() != nil:
		if m.currentDB().conn == nil {
			return []hint{{"r", "reconectar"}, {"x", "fechar aba"}, {"ctrl+b", "Início"}}
		}
		return []hint{{"ctrl+b", "Início"}, {"F5", "roda"}, {"tab", "lista/editor/resultado"}, {"esc", "volta"}}
	case m.current() != nil:
		if done, _ := m.current().Exited(); done {
			return []hint{{"enter", "reconectar"}, {"x", "fechar aba"}, {"ctrl+b", "Início"}}
		}
		if v := m.sftpOf(m.current()); v != nil && v.on {
			return []hint{{"ctrl+b", "Início"}, {"alt+s", "Ssh"}, {"esc", "volta"}, {"arrastar do Explorer", "envia"}}
		}
		return []hint{{"ctrl+b", "Início"}, {"alt+s", "Sftp"}, {"esc", "fechar aba"}, {"arrastar", "marca"}, {"botão direito", "copia/cola"}, {"rodinha", "rola"}}
	case m.searching:
		return []hint{{"enter", "ok"}, {"esc", "limpar"}, {"#tag", "busca por tag"}}
	case m.query != "":
		return []hint{{"esc", "limpar filtro"}, {"enter", "conectar"}, {"e", "editar"}, {"/", "buscar"}}
	case m.focus == focusTabs:
		hs := []hint{{"↓", "menu"}, {"q/esc", "sair"}}
		if m.tabCount() > 0 {
			hs = append([]hint{{"→", "sessões"}}, hs...)
		}
		return hs
	case m.dbMode:
		return []hint{{"enter", "conectar"}, {"a", "novo banco"}, {"e", "editar"}, {"d", "apagar"}, {"tab", "menu"}, {"q/esc", "sair"}}
	case m.focus == focusSide:
		return []hint{{"enter", "abrir"}, {"a", "servidor"}, {"n", "pasta"}, {"/", "buscar"}, {"q/esc", "sair"},
			{"e", "editar pasta"}, {"d", "apagar"}, {"tab", "cards"}}
	}
	return []hint{{"enter", "conectar"}, {"a", "novo"}, {"/", "buscar"}, {"q/esc", "sair"}, {"e", "editar"},
		{"n", "pasta"}, {"t", "tela cheia"}, {"d", "apagar"}, {",", "ajustes"}, {"tab", "pastas"}}
}

func (m Model) viewStatus() string {
	// esquerda: resumo e aviso
	summary := fmt.Sprintf(" %d servidores · %d pastas", len(m.cfg.Hosts), len(m.cfg.Folders))
	if m.tab != 0 {
		summary = fmt.Sprintf(" aba %d de %d", m.tab, m.tabCount())
	}
	// versão alinhada com o começo da área principal
	left := sDim.Render(runewidth.FillRight(summary, m.sideW()+2)) + sFaint.Render(m.version)
	if m.toast != "" {
		icon, st := "✓ ", sOk
		if m.toastErr {
			icon, st = "✗ ", sErr
		}
		left += "   " + st.Render(icon+m.toast)
	}

	// direita: atalhos com a tecla em destaque, enquanto couberem
	key := lipgloss.NewStyle().Foreground(cFg).Bold(true)
	budget := m.width - lipgloss.Width(left) - 3
	var parts []string
	used := 0
	for _, h := range m.statusHints() {
		part := key.Render(h.key) + " " + sDim.Render(h.desc)
		w := lipgloss.Width(part)
		if len(parts) > 0 {
			w += 3 // espaço entre atalhos
		}
		if used+w > budget {
			break
		}
		parts = append(parts, part)
		used += w
	}
	right := strings.Join(parts, "   ") + " "

	// aviso grande demais: ele tem prioridade, mas corta pra caber na linha
	if lipgloss.Width(left) > m.width {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(left)
	}
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}
