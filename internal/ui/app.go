package ui

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/config"
	"mtsh/internal/fonts"
	"mtsh/internal/session"
	"mtsh/internal/vault"
)

type focusArea int

const (
	focusSide focusArea = iota
	focusGrid
)

const (
	sideW    = 30 // largura da barra lateral (com a borda)
	cardH    = 6  // altura de um card (com bordas)
	cardMinW = 30
)

type sshDoneMsg struct {
	name   string
	err    error
	reason string // última linha que o ssh escreveu no stderr
}

// tailWriter guarda só o final do que foi escrito, pra extrair o erro do ssh.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	return len(p), nil
}

func (t *tailWriter) lastLine() string {
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

type Model struct {
	cfgPath string
	cfg     *config.File

	width, height int

	focus   focusArea
	open    map[string]bool // pastas abertas na árvore
	ctx     string          // pasta mostrada na área principal ("" = todas)
	sideSel int
	sideOff int
	cardSel int
	gridOff int // primeira linha de cards visível

	searching bool
	query     string

	toast    string
	toastErr bool

	lastClickID string
	lastClickAt time.Time

	modal modal        // janela aberta (nil = nenhuma)
	vault *vault.Vault // senhas salvas

	sessions []*session.Session // abas de sessão
	tab      int                // 0 = Início, i = sessions[i-1]

	iconStatus *fonts.Status // nil enquanto verifica
}

func New(cfgPath string, cfg *config.File, v *vault.Vault) Model {
	zone.NewGlobal()
	m := Model{cfgPath: cfgPath, cfg: cfg, vault: v, open: map[string]bool{}, focus: focusGrid}
	for _, f := range cfg.Folders {
		m.open[f] = true
	}
	return m
}

func (m Model) Init() tea.Cmd { return checkIcons }

// cards devolve os hosts mostrados na área principal.
func (m *Model) cards() []config.Host {
	var out []config.Host
	q := strings.ToLower(m.query)
	for _, h := range m.cfg.Hosts {
		if q != "" {
			hay := strings.ToLower(strings.Join(append([]string{h.Name, h.Host, h.Folder}, h.Tags...), " "))
			if strings.Contains(hay, q) {
				out = append(out, h)
			}
		} else if h.InFolder(m.ctx) {
			out = append(out, h)
		}
	}
	return out
}

// ---- layout ----

func (m *Model) bodyH() int { return max(1, m.height-2) }
func (m *Model) mainW() int { return max(cardMinW, m.width-sideW-3) }
func (m *Model) cols() int  { return min(4, max(1, (m.mainW()+1)/(cardMinW+1))) }
func (m *Model) cardW() int { c := m.cols(); return (m.mainW() - (c - 1)) / c }
func (m *Model) gridRows() int {
	return max(1, (m.bodyH()-2)/cardH)
}
func (m *Model) treeVisible() int { return max(1, m.bodyH()-5) }

// clamp mantém seleções dentro dos limites e a rolagem acompanhando.
func (m *Model) clamp() {
	rows := m.treeRows()
	m.sideSel = min(max(0, m.sideSel), len(rows)-1)
	tv := m.treeVisible()
	if m.sideSel < m.sideOff {
		m.sideOff = m.sideSel
	}
	if m.sideSel >= m.sideOff+tv {
		m.sideOff = m.sideSel - tv + 1
	}

	n := len(m.cards())
	m.cardSel = min(max(0, m.cardSel), max(0, n-1))
	row, vis := m.cardSel/m.cols(), m.gridRows()
	if row < m.gridOff {
		m.gridOff = row
	}
	if row >= m.gridOff+vis {
		m.gridOff = row - vis + 1
	}
}

func (m *Model) say(msg string, isErr bool) { m.toast, m.toastErr = msg, isErr }

// ---- ações ----

// connectFullscreen conecta do jeito antigo: o ssh ocupa o terminal todo
// e o mtsh volta quando a sessão termina.
func (m *Model) connectFullscreen(h config.Host) tea.Cmd {
	c, ctl, err := m.sshCommand(h)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	m.say("conectando em "+h.Name+"…", false)
	tail := &tailWriter{}
	c.Stderr = io.MultiWriter(os.Stderr, tail)
	var finished atomic.Bool
	run := tea.ExecProcess(c, func(err error) tea.Msg {
		finished.Store(true)
		return sshDoneMsg{h.Name, err, tail.lastLine()}
	})
	return tea.Batch(run, detectOS(h, ctl, finished.Load))
}

// activateSide aplica a linha selecionada da árvore na área principal.
// toggle=true abre/fecha pasta ou conecta no host.
func (m *Model) activateSide(toggle bool) tea.Cmd {
	rows := m.treeRows()
	if m.sideSel >= len(rows) {
		return nil
	}
	r := rows[m.sideSel]
	m.query = ""
	switch r.kind {
	case rowAll:
		m.ctx = ""
	case rowFolder:
		if toggle {
			m.open[r.path] = !m.open[r.path]
		}
		m.ctx = r.path
		m.cardSel, m.gridOff = 0, 0
	case rowHost:
		m.ctx = r.host.Folder
		for i, h := range m.cards() {
			if h.Name == r.host.Name {
				m.cardSel = i
			}
		}
		if toggle {
			return m.connect(r.host)
		}
	}
	return nil
}

// persist grava a config; em caso de erro mostra no rodapé e devolve false.
func (m *Model) persist() bool {
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.say("erro ao salvar: "+err.Error(), true)
		return false
	}
	return true
}

func (m *Model) selectCard(name string) {
	for i, h := range m.cards() {
		if h.Name == name {
			m.cardSel = i
		}
	}
}

func (m *Model) selectTreeFolder(path string) {
	for i, r := range m.treeRows() {
		if (path == "" && r.kind == rowAll) || (r.kind == rowFolder && r.path == path) {
			m.sideSel = i
		}
	}
}

// currentFolder é a pasta onde um novo item deve nascer.
func (m *Model) currentFolder() string {
	if m.focus == focusSide {
		rows := m.treeRows()
		switch r := rows[m.sideSel]; r.kind {
		case rowFolder:
			return r.path
		case rowHost:
			return r.host.Folder
		}
		return ""
	}
	return m.ctx
}

// selectedHost devolve o servidor selecionado (na árvore ou nos cards).
func (m *Model) selectedHost() (config.Host, bool) {
	if m.focus == focusSide {
		if r := m.treeRows()[m.sideSel]; r.kind == rowHost {
			return r.host, true
		}
		return config.Host{}, false
	}
	cards := m.cards()
	if m.cardSel < len(cards) {
		return cards[m.cardSel], true
	}
	return config.Host{}, false
}

func (m *Model) openNewHost() { m.modal = newHostModal(m, nil, m.currentFolder()) }

func (m *Model) openEdit(h config.Host) { m.modal = newHostModal(m, &h, h.Folder) }

func (m *Model) openNewFolder() { m.modal = newFolderModal(m.currentFolder()) }

// askDelete pergunta antes de apagar o servidor ou a pasta selecionada.
func (m *Model) askDelete() {
	if m.focus == focusSide {
		if r := m.treeRows()[m.sideSel]; r.kind == rowFolder {
			m.askDeleteFolder(r.path)
			return
		}
	}
	h, ok := m.selectedHost()
	if !ok {
		return
	}
	m.modal = &confirmModal{
		title: "Apagar servidor", okLabel: "Apagar",
		text: "Apagar " + h.Name + "?\nA chave e a senha salvas dele também somem.",
		onOK: func(m *Model) tea.Cmd {
			m.deleteHost(h)
			return nil
		},
	}
}

func (m *Model) askDeleteFolder(path string) {
	if n := m.cfg.Count(path); n > 0 {
		m.say(fmt.Sprintf("a pasta %s tem %d servidores; mova ou apague eles antes", config.Base(path), n), true)
		return
	}
	m.modal = &confirmModal{
		title: "Apagar pasta", okLabel: "Apagar",
		text: "Apagar a pasta " + config.Base(path) + "?",
		onOK: func(m *Model) tea.Cmd {
			m.cfg.DeleteFolder(path)
			if m.persist() {
				m.ctx = config.Parent(path)
				m.selectTreeFolder(m.ctx)
				m.say("pasta "+config.Base(path)+" apagada", false)
			}
			return nil
		},
	}
}

func (m *Model) deleteHost(h config.Host) {
	var keep []config.Host
	for _, x := range m.cfg.Hosts {
		if x.Name != h.Name {
			keep = append(keep, x)
		}
	}
	m.cfg.Hosts = keep
	if !m.persist() {
		return
	}
	if h.Key != "" && filepath.Dir(config.ExpandHome(h.Key)) == config.KeysDir(m.cfgPath) {
		os.Remove(config.ExpandHome(h.Key))
	}
	if err := m.vault.Delete(h.Name); err != nil {
		m.say(h.Name+" apagado, mas a senha continuou salva: "+err.Error(), true)
		return
	}
	m.say(h.Name+" apagado", false)
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if mm, ok := msg.(tea.MouseMsg); ok {
		// só aparece com MTSH_DEBUG=arquivo.log
		log.Printf("mouse: x=%d y=%d botão=%v ação=%v", mm.X, mm.Y, mm.Button, mm.Action)
	}
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		cols, rows := m.termSize()
		for _, s := range m.sessions {
			s.Resize(cols, rows)
		}

	case sessionOutputMsg:
		// a tela é redesenhada sozinha; só volta a escutar
		cmd = listen(msg.s)

	case sessionExitMsg:
		m.onSessionExit(msg.s)

	case osDetectedMsg:
		m.onOSDetected(msg)

	case iconStatusMsg:
		m.onIconStatus(msg.st)

	case iconInstallMsg:
		cmd = m.onIconInstall(msg)

	case sshDoneMsg:
		var ee *exec.ExitError
		switch {
		case msg.err == nil:
			m.say("sessão em "+msg.name+" encerrada", false)
		case errors.As(msg.err, &ee) && ee.ExitCode() == 255:
			reason := msg.reason
			if reason == "" {
				reason = "o ssh saiu com erro"
			}
			m.say("não deu pra conectar em "+msg.name+": "+reason, true)
		case errors.As(msg.err, &ee):
			m.say(fmt.Sprintf("sessão em %s encerrada (código %d)", msg.name, ee.ExitCode()), false)
		default:
			m.say("erro ao abrir o ssh: "+msg.err.Error(), true)
		}
		// o Bubble Tea (v1.3.x) não religa o mouse ao voltar de um ExecProcess
		cmd = tea.EnableMouseCellMotion

	case tea.MouseMsg:
		if m.modal != nil {
			cmd = m.updateModal(msg)
		} else {
			cmd = m.handleMouse(msg)
		}

	case tea.KeyMsg:
		switch {
		case m.modal != nil:
			cmd = m.updateModal(msg)
		case m.current() != nil:
			// numa aba de sessão o teclado vai pro servidor (inclusive ctrl+c)
			cmd = m.sessionKey(msg)
		case msg.String() == "ctrl+c":
			cmd = m.askQuit()
		case m.searching:
			m.handleSearchKey(msg)
		default:
			cmd = m.handleKey(msg)
		}

	default:
		// piscar do cursor e outras mensagens dos campos de texto
		if m.modal != nil {
			cmd = m.updateModal(msg)
		}
	}
	m.clamp()
	return m, cmd
}

func (m *Model) updateModal(msg tea.Msg) tea.Cmd {
	cur := m.modal
	cmd, done := cur.update(m, msg)
	// a própria janela pode ter trocado m.modal (ex: abrir outra janela)
	if done && m.modal == cur {
		m.modal = nil
	}
	return cmd
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		m.searching, m.query = false, ""
	case tea.KeyEnter:
		m.searching = false
		m.focus = focusGrid
	case tea.KeyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.query += string(msg.Runes)
		m.cardSel, m.gridOff = 0, 0
	}
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	m.toast = ""
	k := msg.String()
	if len(k) == 1 && k >= "1" && k <= "9" {
		if i := int(k[0] - '0'); i <= len(m.sessions) {
			m.tab = i
		}
		return nil
	}
	switch k {
	case "q":
		return m.askQuit()
	case ",":
		m.modal = &settingsModal{}
		return checkIcons
	case "t":
		// conectar em tela cheia, do jeito antigo
		if h, ok := m.selectedHost(); ok {
			return m.connectFullscreen(h)
		}
		return nil
	case "tab", "shift+tab":
		if m.focus == focusSide {
			m.focus = focusGrid
		} else {
			m.focus = focusSide
		}
		return nil
	case "/":
		m.searching, m.query = true, ""
		return nil
	case "a":
		m.openNewHost()
		return nil
	case "n":
		m.openNewFolder()
		return nil
	case "e":
		if m.focus == focusSide {
			if r := m.treeRows()[m.sideSel]; r.kind == rowFolder {
				m.modal = editFolderModal(m, r.path)
				return nil
			}
		}
		if h, ok := m.selectedHost(); ok {
			m.openEdit(h)
		}
		return nil
	case "d", "delete":
		m.askDelete()
		return nil
	case "esc":
		m.query = ""
		return nil
	}

	if m.focus == focusSide {
		rows := m.treeRows()
		switch k {
		case "up", "k":
			m.sideSel--
			m.clamp()
			return m.activateSide(false)
		case "down", "j":
			m.sideSel++
			m.clamp()
			return m.activateSide(false)
		case "enter", " ":
			return m.activateSide(true)
		case "right", "l":
			if r := rows[m.sideSel]; r.kind == rowFolder && !m.open[r.path] {
				m.open[r.path] = true
			} else {
				m.focus = focusGrid
			}
		case "left", "h":
			r := rows[m.sideSel]
			if r.kind == rowFolder && m.open[r.path] {
				m.open[r.path] = false
			} else if p := parentOf(r); p != "" {
				for i, x := range rows {
					if x.kind == rowFolder && x.path == p {
						m.sideSel = i
					}
				}
				m.open[p] = false
				m.ctx = p
			}
		}
		return nil
	}

	cards := m.cards()
	cols := m.cols()
	switch k {
	case "right", "l":
		m.cardSel++
	case "left", "h":
		if m.cardSel%cols == 0 {
			m.focus = focusSide
		} else {
			m.cardSel--
		}
	case "down", "j":
		if m.cardSel+cols < len(cards) {
			m.cardSel += cols
		}
	case "up", "k":
		if m.cardSel-cols >= 0 {
			m.cardSel -= cols
		}
	case "enter":
		if m.cardSel < len(cards) {
			return m.connect(cards[m.cardSel])
		}
	}
	return nil
}

func parentOf(r treeRow) string {
	switch r.kind {
	case rowFolder:
		return config.Parent(r.path)
	case rowHost:
		return r.host.Folder
	}
	return ""
}

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return nil
		}
		d := 1
		if msg.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		if m.current() != nil {
			return nil
		}
		if msg.X < sideW {
			m.sideSel += d
			m.clamp()
			return m.activateSide(false)
		}
		m.cardSel += d * m.cols()
		return nil
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return nil
		}
	default:
		return nil
	}

	// abas
	for i := 0; i <= len(m.sessions); i++ {
		if i > 0 && zone.Get(fmt.Sprintf("tabx:%d", i)).InBounds(msg) {
			m.closeTab(i)
			return nil
		}
		if zone.Get(fmt.Sprintf("tab:%d", i)).InBounds(msg) {
			m.tab = i
			return nil
		}
	}
	if m.current() != nil {
		// na aba de sessão só a barra lateral responde ao clique (volta pro Início)
		if msg.X >= sideW {
			return nil
		}
		m.tab = 0
	}

	m.toast = ""
	if zone.Get("act:new").InBounds(msg) {
		m.focus = focusGrid
		m.openNewHost()
		return nil
	}
	if zone.Get("act:settings").InBounds(msg) {
		m.tab = 0
		m.modal = &settingsModal{}
		return checkIcons
	}
	if zone.Get("act:editfolder").InBounds(msg) && m.ctx != "" {
		m.modal = editFolderModal(m, m.ctx)
		return nil
	}
	if zone.Get("act:folder").InBounds(msg) {
		m.openNewFolder()
		return nil
	}
	if zone.Get("act:search").InBounds(msg) {
		m.searching, m.query = true, ""
		return nil
	}
	for i := range m.treeRows() {
		if zone.Get(fmt.Sprintf("tree:%d", i)).InBounds(msg) {
			m.focus = focusSide
			m.sideSel = i
			// clicar numa pasta já selecionada abre/fecha; num host, dois cliques conectam
			id := fmt.Sprintf("tree:%d", i)
			again := m.lastClickID == id
			dbl := again && time.Since(m.lastClickAt) < 400*time.Millisecond
			m.lastClickID, m.lastClickAt = id, time.Now()
			r := m.treeRows()[i]
			return m.activateSide((r.kind == rowFolder && again) || (r.kind == rowHost && dbl))
		}
	}
	for i, h := range m.cards() {
		z := zone.Get(fmt.Sprintf("card:%d", i))
		if !z.InBounds(msg) {
			continue
		}
		m.focus = focusGrid
		m.cardSel = i
		x, y := z.Pos(msg)
		// linha dos botões: borda(1) + 3 linhas de conteúdo
		if y == 4 {
			if x >= 2 && x < 2+len(btnConnect) {
				return m.connect(h)
			}
			if x >= 3+len(btnConnect) && x < 3+len(btnConnect)+len(btnEdit) {
				m.openEdit(h)
				return nil
			}
		}
		id := fmt.Sprintf("card:%d", i)
		if m.lastClickID == id && time.Since(m.lastClickAt) < 400*time.Millisecond {
			m.lastClickID = ""
			return m.connect(h)
		}
		m.lastClickID, m.lastClickAt = id, time.Now()
		return nil
	}
	return nil
}
