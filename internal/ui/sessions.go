package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/askpass"
	"mtsh/internal/config"
	"mtsh/internal/osinfo"
	"mtsh/internal/session"
)

// tab 0 é sempre o Início; a aba i (i >= 1) é m.sessions[i-1].

type sessionOutputMsg struct{ s *session.Session }
type sessionExitMsg struct{ s *session.Session }

// listen espera a próxima saída (ou o fim) de uma sessão.
func listen(s *session.Session) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-s.Updates(); !ok {
			return sessionExitMsg{s}
		}
		return sessionOutputMsg{s}
	}
}

// sshCommand monta o comando ssh, já com a senha salva (via askpass) se houver,
// e com um socket de controle (ctl) pra detectar o sistema depois.
func (m *Model) sshCommand(h config.Host) (c *exec.Cmd, ctl string, err error) {
	ctl, err = controlPath()
	if err != nil {
		return nil, "", fmt.Errorf("não deu pra preparar a conexão: %w", err)
	}
	args := append([]string{"-o", "ControlMaster=yes", "-o", "ControlPath=" + ctl, "-o", "ControlPersist=no"}, h.SSHArgs()...)
	c = exec.Command("ssh", args...)
	if h.AuthType() == config.AuthPassword && m.vault.Has(h.Name) {
		pass, err := m.vault.Get(h.Name)
		if err != nil {
			return nil, "", fmt.Errorf("não deu pra ler a senha de %s: %w", h.Name, err)
		}
		self, err := os.Executable()
		if err != nil {
			return nil, "", fmt.Errorf("não achei o executável do mtsh: %w", err)
		}
		c.Env = append(os.Environ(), askpass.Env(self, pass)...)
	}
	return c, ctl, nil
}

// termSize é o espaço da aba de sessão (tudo à direita da barra lateral).
// A primeira linha da área é a barra de título da sessão; o terminal fica embaixo.
func (m *Model) termSize() (cols, rows int) {
	return max(10, m.width-m.sideW()-1), max(3, m.bodyH()-1)
}

// sessionHost é o servidor da aba de sessão aberta ("" no Início).
func (m *Model) sessionHost() string {
	if s := m.current(); s != nil {
		return s.Name
	}
	return ""
}

// hasSession diz se o servidor tem uma sessão aberta (e ainda conectada).
func (m *Model) hasSession(name string) bool {
	for _, s := range m.sessions {
		if done, _ := s.Exited(); s.Name == name && !done {
			return true
		}
	}
	return false
}

// sessionHeader é a barra de título da sessão: onde você está, sempre à vista.
func (m Model) sessionHeader(s *session.Session, cols int) string {
	bg := lipgloss.NewStyle().Background(cSel)
	// Ssh | Sftp na ponta direita: clicável, ou alt+s
	sftpOn := false
	if v := m.sftpOf(s); v != nil {
		sftpOn = v.on
	}
	seg := func(id, label string, on bool) string {
		if on {
			return zone.Mark(id, lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Render(label))
		}
		return zone.Mark(id, bg.Foreground(cDim).Render(label))
	}
	toggle := seg("sess:ssh", " Ssh ", !sftpOn) + seg("sess:sftp", " Sftp ", sftpOn) + bg.Render(" ")
	cols -= lipgloss.Width(toggle)
	h, ok := m.hostByName(s.Name)
	if !ok {
		return bg.Foreground(cFg).Bold(true).Width(cols).Render(" "+s.Name) + toggle
	}
	mark := lipgloss.NewStyle().Background(cSel).Foreground(cDim).Render(m.markChar(h))
	if d, ok := distroOf(h); ok && !m.cfg.SimpleIcons {
		mark = lipgloss.NewStyle().Background(cSel).Foreground(d.color).Render(d.glyph)
	} else if ok {
		mark = lipgloss.NewStyle().Background(cSel).Foreground(d.color).Render("●")
	}
	target := h.Target()
	if h.Port != 0 && h.Port != 22 {
		target += fmt.Sprintf(":%d", h.Port)
	}
	parts := []string{
		bg.Render(" ") + mark + bg.Foreground(cAcc).Bold(true).Render(" "+h.Name),
		bg.Foreground(cFg).Render(target),
	}
	if h.Folder != "" {
		st := m.cfg.Style(h.Folder)
		parts = append(parts, bg.Foreground(folderColor(st.Color)).Render(strings.ReplaceAll(h.Folder, "/", " › ")))
	}
	if h.OSName != "" {
		parts = append(parts, bg.Foreground(cDim).Render(h.OSName))
	}
	if done, _ := s.Exited(); done {
		parts = append(parts[:2], bg.Foreground(cErr).Render("desconectado"))
	}
	// sem espaço, larga as partes do fim (distro, depois pasta) e encurta o resto
	sep := bg.Foreground(cFaint).Render("  ·  ")
	line := strings.Join(parts, sep)
	for len(parts) > 1 && lipgloss.Width(line) > cols {
		parts = parts[:len(parts)-1]
		line = strings.Join(parts, sep)
	}
	if lipgloss.Width(line) > cols {
		line = lipgloss.NewStyle().MaxWidth(cols).Render(line)
	}
	if w := lipgloss.Width(line); w < cols {
		line += bg.Render(strings.Repeat(" ", cols-w))
	}
	return line + toggle
}

func (m *Model) current() *session.Session {
	if m.tab < 1 || m.tab > len(m.sessions) {
		return nil
	}
	return m.sessions[m.tab-1]
}

// connect abre o servidor numa aba (ou vai pra aba dele, se já estiver aberta).
func (m *Model) connect(h config.Host) tea.Cmd {
	for i, s := range m.sessions {
		if done, _ := s.Exited(); s.Name == h.Name && !done {
			m.tab = i + 1
			return nil
		}
	}
	c, ctl, err := m.sshCommand(h)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	cols, rows := m.termSize()
	s, err := session.Start(h.Name, c, cols, rows)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	m.sessions = append(m.sessions, s)
	m.sftp[s] = newSftpView(ctl, h.Target())
	m.tab = len(m.sessions)
	m.toast = ""
	return tea.Batch(listen(s), detectOS(h, ctl, func() bool { done, _ := s.Exited(); return done }))
}

// reconnect troca uma sessão encerrada por uma nova, na mesma aba.
func (m *Model) reconnect(i int) tea.Cmd {
	old := m.sessions[i-1]
	h, ok := m.hostByName(old.Name)
	if !ok {
		m.say(old.Name+" não existe mais na lista", true)
		return nil
	}
	c, ctl, err := m.sshCommand(h)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	cols, rows := m.termSize()
	s, err := session.Start(h.Name, c, cols, rows)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	m.sessions[i-1] = s
	nv := newSftpView(ctl, h.Target())
	if v := m.sftp[old]; v != nil {
		nv.adopt(v) // transferências paradas, pastas e a vista vêm junto
		v.close()
		delete(m.sftp, old)
	}
	m.sftp[s] = nv
	m.toast = ""
	var sftpCmd tea.Cmd
	if nv.on { // estava no SFTP: reabre conectado
		nv.on = false
		sftpCmd = m.toggleSftp(s)
	}
	return tea.Batch(listen(s), detectOS(h, ctl, func() bool { done, _ := s.Exited(); return done }), sftpCmd)
}

func (m *Model) hostByName(name string) (config.Host, bool) {
	for _, h := range m.cfg.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return config.Host{}, false
}

func (m *Model) closeTab(i int) {
	if i < 1 || i > len(m.sessions) {
		return
	}
	m.sessions[i-1].Close()
	if v := m.sftp[m.sessions[i-1]]; v != nil {
		v.close()
		delete(m.sftp, m.sessions[i-1])
	}
	m.sessions = append(m.sessions[:i-1], m.sessions[i:]...)
	switch {
	case m.tab == i:
		m.tab = 0 // fechou a aba que estava aberta: volta pro Início
	case m.tab > i:
		m.tab--
	}
}

func (m *Model) indexOf(s *session.Session) int {
	for i, x := range m.sessions {
		if x == s {
			return i + 1
		}
	}
	return 0
}

func (m *Model) aliveSessions() int {
	n := 0
	for _, s := range m.sessions {
		if done, _ := s.Exited(); !done {
			n++
		}
	}
	return n
}

// onSessionExit: saída normal (exit) fecha a aba; erro mantém a aba aberta
// mostrando a mensagem do ssh.
func (m *Model) onSessionExit(s *session.Session) {
	i := m.indexOf(s)
	if i == 0 {
		return // aba já foi fechada
	}
	if code := s.ExitCode(); code == 0 {
		m.closeTab(i)
		m.say("sessão em "+s.Name+" encerrada", false)
	} else if code == 255 {
		m.say("não deu pra conectar em "+s.Name+" · enter tenta de novo", true)
	} else {
		m.say(fmt.Sprintf("sessão em %s encerrada (código %d)", s.Name, code), false)
	}
}

// askQuit confirma antes de sair com sessões abertas.
func (m *Model) askQuit() tea.Cmd {
	n := m.aliveSessions()
	// sem sessões abertas, enter já sai; com sessões, enter cancela pra não derrubar conexão sem querer
	text := "Fechar o mtsh?"
	if n > 0 {
		text = fmt.Sprintf("Tem %d sessão(ões) aberta(s).\nSair encerra todas.", n)
	}
	m.modal = &confirmModal{
		title: "Sair do mtsh", okLabel: "Sair", focusOK: n == 0, text: text,
		onOK: func(m *Model) tea.Cmd {
			for _, s := range m.sessions {
				s.Close()
			}
			return tea.Quit
		},
	}
	return nil
}

// askCloseTab confirma antes de fechar a aba (e a conexão) de s.
func (m *Model) askCloseTab(s *session.Session) {
	m.modal = &confirmModal{
		title: "Fechar aba", okLabel: "Fechar", focusOK: true,
		text: "Fechar a sessão em " + s.Name + "?\nA conexão é encerrada.",
		onOK: func(m *Model) tea.Cmd {
			m.closeTab(m.indexOf(s)) // índice de agora: outra aba pode ter fechado nesse meio-tempo
			return nil
		},
	}
}

// sessionKey trata o teclado numa aba de sessão.
func (m *Model) sessionKey(k tea.KeyMsg) tea.Cmd {
	s := m.current()
	if k.String() == "ctrl+b" {
		m.tab = 0
		return nil
	}
	if k.String() == "alt+s" {
		return m.toggleSftp(s)
	}
	if v := m.sftpOf(s); v != nil && v.on {
		return m.sftpKey(v, k)
	}
	if done, _ := s.Exited(); done {
		switch k.String() {
		case "enter", "r":
			return m.reconnect(m.tab)
		case "x", "esc", "q", "ctrl+w":
			m.closeTab(m.tab)
		}
		return nil
	}
	// rolagem pelo teclado (só fora da tela cheia, onde as teclas são do programa)
	if !s.AltScreen() {
		switch k.String() {
		case "esc":
			// na tela cheia (vim, less, htop) o esc é do programa; no shell, pergunta se fecha
			m.askCloseTab(s)
			return nil
		case "shift+up":
			s.ScrollBy(1)
			return nil
		case "shift+down":
			s.ScrollBy(-1)
			return nil
		}
	}
	// colagem pelo terminal (ctrl+v, ctrl+shift+v): chega como um bloco só
	if k.Paste {
		s.ClearSelection()
		s.Paste(string(k.Runes))
		return nil
	}
	// qualquer tecla digitada volta pro fim e desmarca, como num terminal normal
	s.ClearSelection()
	s.ScrollToBottom()
	s.Write(encodeKey(k, s.AppCursor()))
	return nil
}

// scrollSession rola a aba: n > 0 sobe. Na tela cheia (vim, less, htop) não há
// histórico; aí a rodinha vira setas pro programa, como nos terminais normais.
func (m *Model) scrollSession(s *session.Session, n int) {
	if s.AltScreen() {
		if done, _ := s.Exited(); done {
			return
		}
		key := tea.KeyMsg{Type: tea.KeyDown}
		if n > 0 {
			key = tea.KeyMsg{Type: tea.KeyUp}
		}
		for i := 0; i < abs(n); i++ {
			s.Write(encodeKey(key, s.AppCursor()))
		}
		return
	}
	s.ScrollBy(n)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// sessionCSI trata sequências que o Bubble Tea não reconhece: shift+PgUp/PgDn
// rolam uma página do histórico.
func (m *Model) sessionCSI(s *session.Session, seq string) {
	if s.AltScreen() {
		s.Write([]byte(seq)) // na tela cheia, a tecla é do programa
		return
	}
	_, rows := m.termSize()
	switch seq {
	case "\x1b[5;2~": // shift+PgUp
		s.ScrollBy(rows - 1)
	case "\x1b[6;2~": // shift+PgDn
		s.ScrollBy(-(rows - 1))
	default:
		s.ScrollToBottom()
		s.Write([]byte(seq))
	}
}

// unknownCSI pega a sequência de uma tecla que o Bubble Tea v1 não conhece.
// O tipo dela não é exportado, então a identificação é pelo nome do tipo.
func unknownCSI(msg tea.Msg) (string, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Type().Name() != "unknownCSISequenceMsg" || v.Kind() != reflect.Slice {
		return "", false
	}
	return string(v.Bytes()), true
}

// ---- desenho ----

func (m Model) viewSession() string {
	s := m.current()
	cols, rows := m.termSize()
	done, _ := s.Exited()
	if v := m.sftpOf(s); v != nil && v.on {
		body := m.viewSftp(v, cols, rows)
		return lipgloss.NewStyle().PaddingLeft(1).Render(m.sessionHeader(s, cols) + "\n" + strings.Join(body, "\n"))
	}
	lines := s.Lines(!done && m.modal == nil)
	for len(lines) < rows {
		lines = append(lines, "")
	}
	lines = lines[:rows]
	header := m.sessionHeader(s, cols)
	if off, hist := s.Scroll(); off > 0 {
		msg := fmt.Sprintf(" ↑ histórico  ·  %d de %d linhas acima  ·  rodinha ou shift+↓ desce  ·  digitar volta pro fim ", off, hist)
		lines[rows-1] = lipgloss.NewStyle().Background(cLine).Foreground(cFg).Width(cols).Render(msg)
	} else if done {
		msg := " Sessão encerrada  ·  enter reconecta  ·  x fecha a aba  ·  ctrl+b Início "
		if s.ExitCode() == 255 {
			msg = " Não conectou  ·  enter tenta de novo  ·  x fecha a aba  ·  ctrl+b Início "
		}
		lines[rows-1] = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Width(cols).Render(msg)
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(header + "\n" + strings.Join(lines, "\n"))
}

func (m Model) viewTabLabels() string {
	// aba ativa em destaque (fundo âmbar); nas outras, o símbolo vem colorido
	tab := func(i int, mark, label string) string {
		id := fmt.Sprintf("tab:%d", i)
		if i == m.tab && m.focus == focusTabs {
			// com o foco na barra: mesma largura, com as setinhas dos botões
			return zone.Mark(id, sTabOn.Padding(0, 1).Render("›"+strings.TrimSpace(mark+" "+label)+"‹"))
		}
		if i == m.tab {
			return zone.Mark(id, sTabOn.Render(strings.TrimSpace(mark+" "+label)))
		}
		if mark == "" {
			return zone.Mark(id, sTabOff.Render(label))
		}
		return zone.Mark(id, lipgloss.NewStyle().PaddingLeft(2).Render(m.tabMark(i))+sTabOff.PaddingLeft(1).Render(label))
	}
	out := tab(0, "", "Início")
	for i, s := range m.sessions {
		x := zone.Mark(fmt.Sprintf("tabx:%d", i+1), sFaint.Render(" ×"))
		out += tab(i+1, m.tabMarkPlain(i+1), s.Name) + x
	}
	return out
}

// tabMarkPlain é o símbolo da aba sem cor (pra aba ativa, de estilo único):
// logo da distro, ou ○ se a sessão caiu.
func (m Model) tabMarkPlain(i int) string {
	s := m.sessions[i-1]
	if done, _ := s.Exited(); done {
		return "○"
	}
	if h, ok := m.hostByName(s.Name); ok {
		return m.markChar(h)
	}
	return "●"
}

// tabMark é o símbolo colorido das abas inativas (vermelho se a sessão caiu).
func (m Model) tabMark(i int) string {
	s := m.sessions[i-1]
	if done, _ := s.Exited(); done {
		return sErr.Render("○")
	}
	if h, ok := m.hostByName(s.Name); ok {
		return m.hostMark(h)
	}
	return sOk.Render("●")
}

// ---- detecção do sistema (distro) ----

type osDetectedMsg struct {
	name string
	info osinfo.Info
}

var ctlSeq atomic.Int64

// controlPath devolve um socket de controle único pra esta conexão. O ssh
// interativo vira "mestre" nele e a detecção do sistema reaproveita o mesmo
// login, sem autenticar de novo. Cada conexão tem o seu socket, então fechar
// uma aba nunca derruba outra.
func controlPath() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("mtsh-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d-%d", os.Getpid(), ctlSeq.Add(1))), nil
}

// detectOS espera o socket de controle aparecer (o login pode demorar, ex:
// digitando senha) e roda osinfo.Command por ele.
func detectOS(h config.Host, ctl string, stop func() bool) tea.Cmd {
	return func() tea.Msg {
		if !waitCtl(ctl, stop) {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "ssh", "-S", ctl, "-o", "ControlMaster=no", "-o", "BatchMode=yes",
			"-T", "--", h.Target(), osinfo.Command).Output()
		info, ok := osinfo.Parse(string(out))
		if !ok {
			log.Printf("detecção de sistema em %s: saída não reconhecida: %q", h.Name, out)
			return nil
		}
		return osDetectedMsg{h.Name, info}
	}
}

// waitCtl espera o socket de controle aparecer; false se desistiu (stop ou 3 min).
func waitCtl(ctl string, stop func() bool) bool {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if _, err := os.Stat(ctl); err == nil {
			return true
		}
		if stop() || time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// onOSDetected grava a distro no host (se mudou).
func (m *Model) onOSDetected(msg osDetectedMsg) {
	for i, h := range m.cfg.Hosts {
		if h.Name != msg.name {
			continue
		}
		if h.OS == msg.info.ID && h.OSLike == msg.info.Like && h.OSName == msg.info.Name {
			return
		}
		m.cfg.Hosts[i].OS, m.cfg.Hosts[i].OSLike, m.cfg.Hosts[i].OSName = msg.info.ID, msg.info.Like, msg.info.Name
		m.persist()
		return
	}
}
