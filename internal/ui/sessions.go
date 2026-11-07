package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
func (m *Model) termSize() (cols, rows int) {
	return max(10, m.width-sideW-1), max(3, m.bodyH())
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
	m.toast = ""
	return tea.Batch(listen(s), detectOS(h, ctl, func() bool { done, _ := s.Exited(); return done }))
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
	if n == 0 {
		return tea.Quit
	}
	m.modal = &confirmModal{
		title: "Sair do mtsh", okLabel: "Sair",
		text: fmt.Sprintf("Tem %d sessão(ões) aberta(s).\nSair encerra todas.", n),
		onOK: func(m *Model) tea.Cmd {
			for _, s := range m.sessions {
				s.Close()
			}
			return tea.Quit
		},
	}
	return nil
}

// sessionKey trata o teclado numa aba de sessão.
func (m *Model) sessionKey(k tea.KeyMsg) tea.Cmd {
	s := m.current()
	if k.String() == "ctrl+b" {
		m.tab = 0
		return nil
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
	s.Write(encodeKey(k, s.AppCursor()))
	return nil
}

// ---- desenho ----

func (m Model) viewSession() string {
	s := m.current()
	cols, rows := m.termSize()
	done, _ := s.Exited()
	lines := s.Lines(!done && m.modal == nil)
	for len(lines) < rows {
		lines = append(lines, "")
	}
	lines = lines[:rows]
	if done {
		msg := " Sessão encerrada  ·  enter reconecta  ·  x fecha a aba  ·  ctrl+b Início "
		if s.ExitCode() == 255 {
			msg = " Não conectou  ·  enter tenta de novo  ·  x fecha a aba  ·  ctrl+b Início "
		}
		lines[rows-1] = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Width(cols).Render(msg)
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(strings.Join(lines, "\n"))
}

func (m Model) viewTabLabels() string {
	tab := func(i int, label string) string {
		st := lipgloss.NewStyle().Foreground(cDim).Padding(0, 2)
		if i == m.tab {
			st = sTabOn
		}
		return zone.Mark(fmt.Sprintf("tab:%d", i), st.Render(label))
	}
	out := tab(0, "Início")
	for i, s := range m.sessions {
		dot := sOk.Render("●")
		if done, _ := s.Exited(); done {
			dot = sErr.Render("○")
		}
		x := zone.Mark(fmt.Sprintf("tabx:%d", i+1), sFaint.Render(" ×"))
		out += tab(i+1, dot+" "+s.Name) + x
	}
	return out
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
		deadline := time.Now().Add(3 * time.Minute)
		for {
			if _, err := os.Stat(ctl); err == nil {
				break
			}
			if stop() || time.Now().After(deadline) {
				return nil
			}
			time.Sleep(300 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "ssh", "-S", ctl, "-o", "ControlMaster=no", "-o", "BatchMode=yes",
			"-T", h.Target(), osinfo.Command).Output()
		info, ok := osinfo.Parse(string(out))
		if !ok {
			log.Printf("detecção de sistema em %s: saída não reconhecida: %q", h.Name, out)
			return nil
		}
		return osDetectedMsg{h.Name, info}
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
