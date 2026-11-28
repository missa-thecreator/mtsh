package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"

	"mtsh/internal/config"
	"mtsh/internal/kube"
	"mtsh/internal/session"
)

// Clusters Kubernetes: os contextos do kubeconfig viram cards (linha Clusters
// do menu). Cada um abre numa aba de terminal (o shell da pessoa, com o kubectl
// já apontando pro cluster) e o alt+v troca pra visualização com os namespaces
// e os pods, como o alt+s troca o SSH pelo SFTP. Shell e logs de um pod abrem
// em abas de terminal próprias.
//
// Tudo passa pelo kubectl da máquina. Nada aqui altera o cluster: só lista,
// descreve, lê logs e abre o shell que a pessoa pediu.

const kubeGlyph = "☸"

// kubeRefresh é de quanto em quanto tempo a lista de pods se atualiza sozinha.
const kubeRefresh = 3 * time.Second

type kubeContextsMsg struct {
	ctxs []kube.Context
	err  error
}

type kubeNSMsg struct {
	v   *kubeView
	ns  []string
	err error
}

type kubePodsMsg struct {
	v    *kubeView
	ns   string // namespace pedido ("" = todos)
	pods []kube.Pod
	err  error
}

type kubeTickMsg struct{ v *kubeView }

type kubeDescribeMsg struct {
	v    *kubeView
	pod  string
	text string
	err  error
}

// loadKubeContexts lê os contextos do kubeconfig (não fala com cluster nenhum).
func loadKubeContexts() tea.Msg {
	if !kube.Available() {
		return kubeContextsMsg{err: errNoKubectl}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := kube.Contexts(ctx)
	return kubeContextsMsg{cs, err}
}

var errNoKubectl = fmt.Errorf("kubectl não encontrado")

// kubeView é uma aba de cluster.
type kubeView struct {
	kctx   kube.Context
	on     bool // mostrando a visualização (senão, o terminal)
	closed bool
	focus  int // 0 namespaces, 1 pods

	nss          []string // namespaces do cluster
	nsLoaded     bool
	ns           string // namespace mostrado ("" = todos)
	nsSel, nsOff int    // lista (0 = "todos")

	pods     []kube.Pod
	podsNS   string // de qual namespace são os pods mostrados
	podsAt   time.Time
	loading  bool
	sel, off int

	msg    string
	msgErr bool

	lastClick time.Time
}

func (v *kubeView) say(text string, isErr bool) { v.msg, v.msgErr = text, isErr }

// nsRows são as linhas da lista de namespaces: "todos" e depois cada um.
func (v *kubeView) nsRows() []string { return append([]string{""}, v.nss...) }

// currentKube é a visualização do cluster, quando é ela que está na tela.
func (m *Model) currentKube() *kubeView {
	s := m.current()
	if s == nil {
		return nil
	}
	if v := m.kubeVis[s]; v != nil && v.on {
		return v
	}
	return nil
}

// clusterSession é a aba de terminal do cluster (nil se não estiver aberta).
func (m *Model) clusterSession(kctx string) *session.Session {
	for _, s := range m.sessions {
		if k := m.kubeSess[s]; k != nil && k.term && k.kctx == kctx {
			return s
		}
	}
	return nil
}

func (m *Model) kubeTabOpen(name string) bool {
	s := m.clusterSession(name)
	if s == nil {
		return false
	}
	done, _ := s.Exited()
	return !done
}

// openKube abre o cluster numa aba de terminal (ou vai pra aba dele, se já estiver aberta).
func (m *Model) openKube(c kube.Context) tea.Cmd {
	if s := m.clusterSession(c.Name); s != nil {
		m.tab = m.indexOf(s)
		return nil
	}
	s, err := m.spawnKube(kubeSession{kctx: c.Name, term: true})
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	v := &kubeView{kctx: c, ns: c.Namespace, focus: 1}
	if v.ns == "" {
		v.ns = "default"
	}
	m.kubeVis[s] = v
	m.sessions = append(m.sessions, s)
	m.tab = len(m.sessions)
	m.toast = ""
	return tea.Batch(listen(s), kubeTick(v))
}

// toggleKubeVisual troca entre o terminal e a visualização do cluster (alt+v).
func (m *Model) toggleKubeVisual(s *session.Session) tea.Cmd {
	v := m.kubeVis[s]
	if v == nil {
		return nil
	}
	v.on = !v.on
	if !v.on {
		return nil
	}
	var cmds []tea.Cmd
	if !v.nsLoaded {
		v.say("conectando em "+v.kctx.Name+"…", false)
		cmds = append(cmds, kubeLoadNS(v))
	}
	return tea.Batch(append(cmds, m.kubeLoadPods(v))...)
}

// forgetKube solta o que o mtsh guardava de uma aba de cluster/pod que fechou.
func (m *Model) forgetKube(s *session.Session) {
	if k := m.kubeSess[s]; k != nil && k.kubeconfig != "" {
		os.Remove(k.kubeconfig)
	}
	if v := m.kubeVis[s]; v != nil {
		v.closed = true
	}
	delete(m.kubeSess, s)
	delete(m.kubeVis, s)
}

func kubeLoadNS(v *kubeView) tea.Cmd {
	kctx := v.kctx.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ns, err := kube.Namespaces(ctx, kctx)
		return kubeNSMsg{v, ns, err}
	}
}

func (m *Model) kubeLoadPods(v *kubeView) tea.Cmd {
	if v.loading {
		return nil
	}
	v.loading = true
	kctx, ns := v.kctx.Name, v.ns
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pods, err := kube.Pods(ctx, kctx, ns)
		return kubePodsMsg{v, ns, pods, err}
	}
}

func kubeTick(v *kubeView) tea.Cmd {
	return tea.Tick(kubeRefresh, func(time.Time) tea.Msg { return kubeTickMsg{v} })
}

func (m *Model) onKubeMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case kubeContextsMsg:
		m.kubeLoaded = true
		m.kubeCtxs, m.kubeErr = msg.ctxs, ""
		if msg.err != nil {
			m.kubeErr = msg.err.Error()
		}
	case kubeNSMsg:
		v := msg.v
		if v.closed {
			return nil
		}
		if msg.err != nil {
			v.say("não deu pra listar os namespaces: "+msg.err.Error(), true)
			return nil
		}
		v.nss, v.nsLoaded = msg.ns, true
		v.nsSel = 0
		for i, n := range v.nsRows() {
			if n == v.ns {
				v.nsSel = i
			}
		}
	case kubePodsMsg:
		v := msg.v
		v.loading = false
		if v.closed {
			return nil
		}
		if msg.ns != v.ns { // trocou de namespace enquanto carregava: pede de novo
			return m.kubeLoadPods(v)
		}
		if msg.err != nil {
			v.say(msg.err.Error(), true)
			return nil
		}
		// mantém o mesmo pod selecionado quando a lista muda
		cur := ""
		if v.sel < len(v.pods) {
			cur = v.pods[v.sel].Namespace + "/" + v.pods[v.sel].Name
		}
		v.pods, v.podsNS, v.podsAt = msg.pods, msg.ns, time.Now()
		v.sel = min(v.sel, max(0, len(v.pods)-1))
		for i, p := range v.pods {
			if p.Namespace+"/"+p.Name == cur {
				v.sel = i
			}
		}
		if v.msgErr || strings.HasPrefix(v.msg, "conectando") {
			v.say("", false)
		}
	case kubeTickMsg:
		v := msg.v
		if v.closed {
			return nil
		}
		// só atualiza a aba que está na tela; as outras esperam a vez
		if m.currentKube() == v && m.modal == nil {
			return tea.Batch(m.kubeLoadPods(v), kubeTick(v))
		}
		return kubeTick(v)
	case kubeDescribeMsg:
		if msg.v.closed {
			return nil
		}
		if msg.err != nil {
			msg.v.say("describe: "+msg.err.Error(), true)
			return nil
		}
		m.modal = &valueModal{title: "describe " + msg.pod, text: msg.text, wide: true}
	}
	return nil
}

// ---- teclado da aba ----

func (m *Model) kubeKey(v *kubeView, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	switch key {
	case "ctrl+b":
		m.tab = 0
		return nil
	case "tab", "shift+tab":
		v.focus = 1 - v.focus
		return nil
	case "esc":
		if v.focus == 1 {
			v.focus = 0
			return nil
		}
		v.on = false // dos namespaces, o esc volta pro terminal
		return nil
	case "ctrl+w":
		if s := m.current(); s != nil {
			m.askCloseTab(s)
		}
		return nil
	case "r", "ctrl+r":
		v.say("atualizando…", false)
		return tea.Batch(kubeLoadNS(v), m.kubeLoadPods(v))
	}
	if v.focus == 0 {
		return m.kubeNSKey(v, key)
	}
	return m.kubePodKey(v, key)
}

func (m *Model) kubeNSKey(v *kubeView, key string) tea.Cmd {
	rows := v.nsRows()
	n := m.kubeListH()
	old := v.nsSel
	switch key {
	case "up", "k":
		v.nsSel--
	case "down", "j":
		v.nsSel++
	case "pgup":
		v.nsSel -= n
	case "pgdown":
		v.nsSel += n
	case "home", "g":
		v.nsSel = 0
	case "end", "G":
		v.nsSel = len(rows) - 1
	case "enter", "right", "l":
		v.focus = 1
		return nil
	}
	v.nsSel = max(0, min(len(rows)-1, v.nsSel))
	v.nsOff = scrollInto(v.nsSel, v.nsOff, n)
	if v.nsSel != old {
		return m.kubeSetNS(v, rows[v.nsSel])
	}
	return nil
}

// kubeSetNS mostra os pods de outro namespace ("" = todos).
func (m *Model) kubeSetNS(v *kubeView, ns string) tea.Cmd {
	if ns == v.ns {
		return nil
	}
	v.ns, v.sel, v.off = ns, 0, 0
	return m.kubeLoadPods(v)
}

func (m *Model) kubePodKey(v *kubeView, key string) tea.Cmd {
	n := m.kubePodsH()
	switch key {
	case "up", "k":
		v.sel--
	case "down", "j":
		v.sel++
	case "pgup":
		v.sel -= n
	case "pgdown":
		v.sel += n
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = len(v.pods) - 1
	case "left", "h":
		v.focus = 0
		return nil
	}
	v.sel = max(0, min(len(v.pods)-1, v.sel))
	v.off = scrollInto(v.sel, v.off, n)
	if v.sel >= len(v.pods) {
		return nil
	}
	p := v.pods[v.sel]
	switch key {
	case "enter":
		m.modal = newKubePodMenu(v, p)
	case "s":
		return m.kubeShell(v, p)
	case "L":
		return m.kubeLogs(v, p)
	case "d":
		return kubeDescribe(v, p)
	case "y":
		return copyCmd(p.Name)
	}
	return nil
}

// scrollInto ajusta a primeira linha à vista (off) pra sel aparecer em n linhas.
func scrollInto(sel, off, n int) int {
	if sel < off {
		return sel
	}
	if sel >= off+n {
		return sel - n + 1
	}
	return off
}

func kubeDescribe(v *kubeView, p kube.Pod) tea.Cmd {
	v.say("describe "+p.Name+"…", false)
	kctx := v.kctx.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := kube.Describe(ctx, kctx, p.Namespace, p.Name)
		return kubeDescribeMsg{v, p.Name, text, err}
	}
}

// ---- shell e logs (abas de terminal) ----

// kubeSession diz o que uma aba de terminal mostra, quando é de um pod.
type kubeSession struct {
	kctx, ns, pod string
	logs          bool // logs em vez de shell
	term          bool // o terminal do cluster (shell local com o kubectl no cluster)

	kubeconfig string // kubeconfig provisório do terminal do cluster (apagado ao fechar)
}

func (k kubeSession) label() string {
	switch {
	case k.term:
		return "terminal"
	case k.logs:
		return "logs"
	}
	return "shell"
}

func (m *Model) kubeShell(v *kubeView, p kube.Pod) tea.Cmd {
	return m.startKubeSession(kubeSession{kctx: v.kctx.Name, ns: p.Namespace, pod: p.Name})
}

func (m *Model) kubeLogs(v *kubeView, p kube.Pod) tea.Cmd {
	return m.startKubeSession(kubeSession{kctx: v.kctx.Name, ns: p.Namespace, pod: p.Name, logs: true})
}

func (k kubeSession) tabName() string {
	switch {
	case k.term:
		return k.kctx
	case k.logs:
		return "logs " + k.pod
	}
	return k.pod
}

// startKubeSession abre o shell ou os logs numa aba nova (ou vai pra aba que já mostra isso).
func (m *Model) startKubeSession(k kubeSession) tea.Cmd {
	for i, s := range m.sessions {
		if x, ok := m.kubeSess[s]; ok && x.kctx == k.kctx && x.ns == k.ns && x.pod == k.pod && x.logs == k.logs && !x.term {
			if done, _ := s.Exited(); !done {
				m.tab = i + 1
				return nil
			}
		}
	}
	s, err := m.spawnKube(k)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	m.sessions = append(m.sessions, s)
	m.tab = len(m.sessions)
	m.toast = ""
	return listen(s)
}

func (m *Model) spawnKube(k kubeSession) (*session.Session, error) {
	var c *exec.Cmd
	switch {
	case k.term:
		var err error
		if c, k.kubeconfig, err = clusterShell(k.kctx); err != nil {
			return nil, err
		}
	case k.logs:
		c = kube.LogsCmd(k.kctx, k.ns, k.pod, "")
	default:
		c = kube.ShellCmd(k.kctx, k.ns, k.pod, "")
	}
	cols, rows := m.termSize()
	s, err := session.Start(k.tabName(), c, cols, rows)
	if err != nil {
		if k.kubeconfig != "" {
			os.Remove(k.kubeconfig)
		}
		return nil, err
	}
	kk := k
	m.kubeSess[s] = &kk
	return s, nil
}

// reconnectKube abre de novo o shell/logs de uma aba encerrada, na mesma aba.
func (m *Model) reconnectKube(i int, k *kubeSession) tea.Cmd {
	old := m.sessions[i-1]
	s, err := m.spawnKube(*k)
	if err != nil {
		m.say(err.Error(), true)
		return nil
	}
	v := m.kubeVis[old]
	if v != nil {
		delete(m.kubeVis, old) // a visualização segue com a aba nova
		m.kubeVis[s] = v
	}
	m.forgetKube(old)
	if v != nil {
		v.closed = false
	}
	m.sessions[i-1] = s
	m.toast = ""
	return listen(s)
}

// kubeSessionHeader é a barra de título de uma aba de shell/logs de pod.
func (m Model) kubeSessionHeader(s *session.Session, k *kubeSession, cols int) string {
	bg := lipgloss.NewStyle().Background(cSel)
	var parts []string
	toggle := ""
	if k.term {
		v := m.kubeVis[s]
		toggle = kubeToggle(v != nil && v.on)
		cols -= lipgloss.Width(toggle)
		parts = []string{bg.Foreground(cAcc).Render(" "+kubeGlyph) + bg.Foreground(cAcc).Bold(true).Render(" "+k.kctx)}
		if v != nil {
			if srv := strings.TrimPrefix(v.kctx.Server, "https://"); srv != "" {
				parts = append(parts, bg.Foreground(cFg).Render(srv))
			}
		}
		parts = append(parts, bg.Foreground(cDim).Render("kubectl neste cluster"))
	} else {
		parts = []string{
			bg.Foreground(cAcc).Render(" "+kubeGlyph) + bg.Foreground(cAcc).Bold(true).Render(" "+k.pod),
			bg.Foreground(cFg).Render(k.label()),
			bg.Foreground(cDim).Render("ns " + k.ns),
			bg.Foreground(cDim).Render(k.kctx),
		}
	}
	if done, _ := s.Exited(); done {
		end := "encerrado"
		if k.logs {
			end = "logs pararam"
		}
		parts = append(parts, bg.Foreground(cErr).Render(end))
	}
	sep := bg.Foreground(cFaint).Render("  ·  ")
	line := strings.Join(parts, sep)
	line = lipgloss.NewStyle().MaxWidth(cols).Render(line)
	if w := lipgloss.Width(line); w < cols {
		line += bg.Render(strings.Repeat(" ", cols-w))
	}
	return line + toggle
}

// kubeToggle é o Terminal | Visual da ponta direita do título (clicável, ou alt+v).
func kubeToggle(visual bool) string {
	bg := lipgloss.NewStyle().Background(cSel)
	seg := func(id, label string, on bool) string {
		if on {
			return zone.Mark(id, lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Render(label))
		}
		return zone.Mark(id, bg.Foreground(cDim).Render(label))
	}
	return seg("sess:term", " Terminal ", !visual) + seg("sess:visual", " Visual ", visual) + bg.Render(" ")
}

// clusterShell abre o shell da pessoa com o kubectl apontando pro cluster:
// um kubeconfig provisório que só diz o contexto atual vai na frente do
// kubeconfig de sempre. O arquivo de verdade não muda, e trocar de contexto
// dentro dessa aba não afeta os outros terminais.
func clusterShell(kctx string) (*exec.Cmd, string, error) {
	f, err := os.CreateTemp("", "mtsh-kube-*.yaml")
	if err != nil {
		return nil, "", fmt.Errorf("não deu pra preparar o terminal do cluster: %w", err)
	}
	name, _ := json.Marshal(kctx) // texto entre aspas também é YAML válido
	_, err = fmt.Fprintf(f, "apiVersion: v1\nkind: Config\ncurrent-context: %s\n", name)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return nil, "", err
	}
	orig := os.Getenv("KUBECONFIG")
	if orig == "" {
		orig = config.ExpandHome("~/.kube/config")
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
		if _, err := exec.LookPath("bash"); err == nil {
			sh = "bash"
		}
	}
	c := exec.Command(sh)
	c.Env = append(os.Environ(), "KUBECONFIG="+f.Name()+string(os.PathListSeparator)+orig, "MTSH_KUBE_CONTEXT="+kctx)
	if home, err := os.UserHomeDir(); err == nil {
		c.Dir = home
	}
	return c, f.Name(), nil
}

// ---- menu do pod ----

type kubePodMenu struct {
	v   *kubeView
	p   kube.Pod
	sel int
}

var kubePodActions = []struct{ key, label, desc string }{
	{"s", "Shell", "abre um terminal dentro do container"},
	{"l", "Logs", "acompanha os logs ao vivo"},
	{"d", "Describe", "detalhes e eventos do pod"},
	{"y", "Copiar nome", "copia o nome do pod"},
}

func newKubePodMenu(v *kubeView, p kube.Pod) *kubePodMenu { return &kubePodMenu{v: v, p: p} }

func (pm *kubePodMenu) do(m *Model, key string) tea.Cmd {
	switch key {
	case "s":
		return m.kubeShell(pm.v, pm.p)
	case "l":
		return m.kubeLogs(pm.v, pm.p)
	case "d":
		return kubeDescribe(pm.v, pm.p)
	case "y":
		return copyCmd(pm.p.Name)
	}
	return nil
}

func (pm *kubePodMenu) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		if clicked("m:close", mm) {
			return nil, true
		}
		for _, a := range kubePodActions {
			if clicked("kp:"+a.key, mm) {
				m.modal = nil
				return pm.do(m, a.key), true
			}
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch key := k.String(); key {
	case "esc", "q":
		return nil, true
	case "up", "k", "shift+tab":
		pm.sel = (pm.sel + len(kubePodActions) - 1) % len(kubePodActions)
	case "down", "j", "tab":
		pm.sel = (pm.sel + 1) % len(kubePodActions)
	case "enter":
		m.modal = nil
		return pm.do(m, kubePodActions[pm.sel].key), true
	default:
		for _, a := range kubePodActions {
			if key == a.key {
				m.modal = nil
				return pm.do(m, key), true
			}
		}
	}
	return nil, false
}

func (pm *kubePodMenu) view(m *Model) string {
	p := pm.p
	rows := []string{
		sDim.Render("  ns "+p.Namespace+" · "+p.Ready+" · ") + kubeStatusStyle(p).Render(p.Status),
	}
	if len(p.Containers) > 1 {
		rows = append(rows, sFaint.Render("  containers: "+strings.Join(p.Containers, ", ")+" (usa o padrão)"))
	}
	rows = append(rows, "")
	for i, a := range kubePodActions {
		line := fmt.Sprintf(" %s  %-12s %s", a.key, a.label, a.desc)
		st := sFg
		if i == pm.sel {
			line = "›" + line[1:]
			st = sRowSelFocus
		}
		rows = append(rows, zone.Mark("kp:"+a.key, st.Render(runewidth.FillRight(line, 56))))
	}
	rows = append(rows, "", sFaint.Render("  enter escolhe · esc fecha"), "")
	return dialog(runewidth.Truncate(p.Name, 54, "…"), 62, strings.Join(rows, "\n"))
}

// ---- cards dos clusters (área principal) ----

func (m Model) viewKubeMain() string {
	w := m.mainW()
	title := sBold.Render("Clusters") + sDim.Render("   "+plural(len(m.kubeCtxs), "cluster", "clusters"))
	header := title
	var grid string
	switch {
	case !m.kubeLoaded:
		grid = sDim.Render("lendo o kubeconfig…")
	case m.kubeErr == errNoKubectl.Error():
		grid = lipgloss.NewStyle().Width(min(w, 70)).Render(
			sBold.Render("kubectl não encontrado.") + "\n\n" +
				sDim.Render("O mtsh usa o kubectl da sua máquina pra falar com os clusters (como usa o ssh pros servidores). ") +
				sDim.Render("Instale o kubectl e configure o acesso; os clusters aparecem aqui sozinhos."))
	case m.kubeErr != "":
		grid = sErr.Render("não deu pra ler o kubeconfig: " + m.kubeErr)
	case len(m.kubeCtxs) == 0:
		grid = lipgloss.NewStyle().Width(min(w, 70)).Render(
			sBold.Render("Nenhum cluster no kubeconfig.") + "\n\n" +
				sDim.Render("Os clusters vêm do mesmo lugar que o kubectl usa (~/.kube/config ou $KUBECONFIG). ") +
				sDim.Render("Configure o acesso no kubectl e eles aparecem aqui."))
	default:
		cols, cw := m.cols(), m.cardW()
		var lines []string
		first := m.gridOff * cols
		last := min(len(m.kubeCtxs), first+m.gridRows()*cols)
		for start := first; start < last; start += cols {
			var row []string
			for i := start; i < min(start+cols, last); i++ {
				if i > start {
					row = append(row, " ")
				}
				row = append(row, zone.Mark(fmt.Sprintf("kubecard:%d", i), m.viewKubeCard(m.kubeCtxs[i], cw, i == m.cardSel)))
			}
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
		}
		grid = strings.Join(lines, "\n") + "\n\n" +
			sFaint.Render("os clusters vêm do seu kubeconfig (o mesmo do kubectl)")
	}
	return lipgloss.NewStyle().PaddingLeft(2).Width(w + 2).Height(m.bodyH()).MaxHeight(m.bodyH()).
		Render(header + "\n\n" + grid)
}

func (m Model) viewKubeCard(c kube.Context, w int, sel bool) string {
	inner := w - 4
	focused := sel && m.focus == focusGrid
	nameStyle := sBold
	if focused {
		nameStyle = nameStyle.Foreground(cAcc)
	}
	name := sAcc.Render(kubeGlyph) + " " + nameStyle.Render(runewidth.Truncate(c.Name, inner-2, "…"))
	tag := ""
	switch {
	case m.kubeTabOpen(c.Name):
		tag = sOk.Render("● aberto")
	case c.Current:
		tag = sFaint.Render("atual")
	}
	if tag != "" {
		if gap := inner - lipgloss.Width(name) - lipgloss.Width(tag); gap > 0 {
			name += strings.Repeat(" ", gap) + tag
		}
	}
	server := strings.TrimPrefix(c.Server, "https://")
	if server == "" {
		server = c.Cluster
	}
	ns := c.Namespace
	if ns == "" {
		ns = "default"
	}
	who := sFaint.Render("ns ") + sDim.Render(runewidth.Truncate(ns, inner/2, "…")) +
		sFaint.Render(" · ") + sDim.Render(runewidth.Truncate(c.User, inner/2-6, "…"))
	connect := sBtnGhost.Render(btnConnect)
	if sel {
		connect = sBtnMain.Render(btnConnect)
	}
	var border lipgloss.TerminalColor = cLine
	if focused {
		border = cAcc
	} else if sel {
		border = cDim
	}
	return lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).Padding(0, 1).Width(w - 2).
		Render(strings.Join([]string{name, sDim.Render(runewidth.Truncate(server, inner, "…")), who, connect}, "\n"))
}

// kubeGridKey trata o teclado nos cards de cluster.
func (m *Model) kubeGridKey(k string) tea.Cmd {
	n, cols := len(m.kubeCtxs), m.cols()
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
		if m.cardSel+cols < n {
			m.cardSel += cols
		}
	case "up", "k":
		if m.cardSel-cols >= 0 {
			m.cardSel -= cols
		}
	case "enter":
		if m.cardSel < n {
			return m.openKube(m.kubeCtxs[m.cardSel])
		}
	case "r", "ctrl+r":
		return loadKubeContexts
	}
	return nil
}

func (m *Model) kubeCardClick(msg tea.MouseMsg) tea.Cmd {
	for i := range m.kubeCtxs {
		if !zone.Get(fmt.Sprintf("kubecard:%d", i)).InBounds(msg) {
			continue
		}
		m.focus = focusGrid
		m.cardSel = i
		_, y := zone.Get(fmt.Sprintf("kubecard:%d", i)).Pos(msg)
		id := fmt.Sprintf("kubecard:%d", i)
		if y == 4 || (m.lastClickID == id && time.Since(m.lastClickAt) < 400*time.Millisecond) {
			m.lastClickID = ""
			return m.openKube(m.kubeCtxs[i])
		}
		m.lastClickID, m.lastClickAt = id, time.Now()
		return nil
	}
	return nil
}

// ---- desenho da aba ----

func (m *Model) kubeListW() int {
	cols, _ := m.termSize()
	return max(16, min(28, cols/5))
}

// kubeListH é quantas linhas a lista de namespaces mostra (tudo menos o título).
func (m *Model) kubeListH() int {
	_, rows := m.termSize()
	return max(1, rows-2-1)
}

// kubePodsH é quantas linhas de pods cabem (menos o título e o cabeçalho).
func (m *Model) kubePodsH() int {
	_, rows := m.termSize()
	return max(1, rows-2-2)
}

func (m Model) viewKubeTab(s *session.Session, v *kubeView) string {
	cols, rows := m.termSize()
	return lipgloss.NewStyle().PaddingLeft(1).Render(m.kubeHeader(v, cols) + "\n" + strings.Join(m.viewKube(v, cols, rows), "\n"))
}

func (m Model) kubeHeader(v *kubeView, cols int) string {
	toggle := kubeToggle(true)
	cols -= lipgloss.Width(toggle)
	bg := lipgloss.NewStyle().Background(cSel)
	parts := []string{
		bg.Foreground(cAcc).Render(" "+kubeGlyph) + bg.Foreground(cAcc).Bold(true).Render(" "+v.kctx.Name),
	}
	if s := strings.TrimPrefix(v.kctx.Server, "https://"); s != "" {
		parts = append(parts, bg.Foreground(cFg).Render(s))
	}
	ns := v.ns
	if ns == "" {
		ns = "todos os namespaces"
	} else {
		ns = "ns " + ns
	}
	parts = append(parts, bg.Foreground(cDim).Render(ns))
	line := strings.Join(parts, bg.Foreground(cFaint).Render("  ·  "))
	line = lipgloss.NewStyle().MaxWidth(cols).Render(line)
	if w := lipgloss.Width(line); w < cols {
		line += bg.Render(strings.Repeat(" ", cols-w))
	}
	return line + toggle
}

func (m Model) viewKube(v *kubeView, cols, rows int) []string {
	listW := m.kubeListW()
	rightW := cols - listW - 1
	top := rows - 2
	left := m.viewKubeNS(v, listW, top)
	right := m.viewKubePods(v, rightW, top)
	sep := sFaint.Render("│")
	out := make([]string, 0, rows)
	for i := 0; i < top; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, lipgloss.NewStyle().Width(listW).MaxWidth(listW).Render(l)+sep+lipgloss.NewStyle().MaxWidth(rightW).Render(r))
	}
	status := sDim.Render(" " + v.msg)
	if v.msgErr {
		status = sErr.Render(" " + v.msg)
	}
	out = append(out, lipgloss.NewStyle().MaxWidth(cols).Render(status))
	help := " enter ações do pod · s shell · L logs · d describe · y copia o nome · tab troca · r atualiza · alt+v terminal"
	out = append(out, sFaint.MaxWidth(cols).Render(help))
	return out
}

func (m Model) viewKubeNS(v *kubeView, w, h int) []string {
	title := lipgloss.NewStyle().Foreground(cDim).Bold(true)
	if v.focus == 0 {
		title = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
	}
	out := []string{title.Render(" Namespaces ")}
	if !v.nsLoaded {
		return append(out, sFaint.Render(" carregando…"))
	}
	rows := v.nsRows()
	n := h - 1
	for i := v.nsOff; i < min(len(rows), v.nsOff+n); i++ {
		label := rows[i]
		if label == "" {
			label = "* todos"
		}
		text := runewidth.FillRight(" "+runewidth.Truncate(label, w-2, "…"), w)
		st := sFg
		switch {
		case i == v.nsSel && v.focus == 0:
			st = sRowSelFocus
		case i == v.nsSel:
			st = sRowSel
		case rows[i] == "":
			st = sDim
		}
		out = append(out, zone.Mark(fmt.Sprintf("kns:%d", i), st.Render(text)))
	}
	return out
}

// kubeStatusStyle pinta o status: verde se está bem, âmbar se está subindo, vermelho se deu problema.
func kubeStatusStyle(p kube.Pod) lipgloss.Style {
	switch {
	case p.Status == "Completed":
		return sDim
	case p.Healthy():
		return sOk
	case p.Status == "Running", p.Status == "Pending", p.Status == "ContainerCreating",
		p.Status == "PodInitializing", p.Status == "Terminating", strings.HasPrefix(p.Status, "Init:"):
		return sAcc
	}
	return sErr
}

func (m Model) viewKubePods(v *kubeView, w, h int) []string {
	title := lipgloss.NewStyle().Foreground(cDim).Bold(true)
	if v.focus == 1 {
		title = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
	}
	info := " Pods "
	if !v.podsAt.IsZero() {
		info = fmt.Sprintf(" Pods · %d ", len(v.pods))
	}
	head := title.Render(info)
	if v.loading && v.podsAt.IsZero() {
		return []string{head, sFaint.Render(" carregando…")}
	}
	if !v.podsAt.IsZero() {
		head += sFaint.Render(fmt.Sprintf("  atualiza a cada %ds", int(kubeRefresh.Seconds())))
	}
	out := []string{head}
	if len(v.pods) == 0 {
		if !v.podsAt.IsZero() {
			out = append(out, sFaint.Render(" nenhum pod neste namespace"))
		}
		return out
	}
	allNS := v.podsNS == ""
	// colunas fixas; o nome fica com o que sobrar
	nsW := 0
	if allNS {
		for _, p := range v.pods {
			nsW = max(nsW, runewidth.StringWidth(p.Namespace))
		}
		nsW = min(nsW, 18)
	}
	stW := 8
	for _, p := range v.pods {
		stW = max(stW, runewidth.StringWidth(p.Status))
	}
	stW = min(stW, 22)
	const readyW, restW, ageW = 7, 9, 6
	nodeW := 0
	for _, p := range v.pods {
		nodeW = max(nodeW, runewidth.StringWidth(p.Node))
	}
	nodeW = min(max(nodeW, 3), 16)
	fixed := readyW + stW + restW + ageW + 2*5
	if allNS {
		fixed += nsW + 2
	}
	nameW := w - 1 - fixed - nodeW - 2
	if nameW < 16 { // sem espaço: some a coluna do nó
		nameW += nodeW + 2
		nodeW = 0
	}
	nameW = max(8, nameW)
	col := func(s string, n int) string { return runewidth.FillRight(runewidth.Truncate(s, n, "…"), n) }
	hdr := " "
	if allNS {
		hdr += col("NAMESPACE", nsW) + "  "
	}
	hdr += col("NOME", nameW) + "  " + col("PRONTOS", readyW) + "  " + col("STATUS", stW) + "  " +
		col("REINÍCIOS", restW) + "  " + col("IDADE", ageW)
	if nodeW > 0 {
		hdr += "  " + col("NÓ", nodeW)
	}
	out = append(out, sBold.Render(hdr))
	now := time.Now()
	n := h - 2
	for i := v.off; i < min(len(v.pods), v.off+n); i++ {
		p := v.pods[i]
		selected := i == v.sel
		cell := func(st lipgloss.Style, s string) string {
			if selected {
				if v.focus == 1 {
					st = st.Background(cSel).Bold(true)
				} else {
					st = st.Background(cSel)
				}
			}
			return st.Render(s)
		}
		gap := cell(sFg, "  ")
		line := cell(sFg, " ")
		if allNS {
			line += cell(sDim, col(p.Namespace, nsW)) + gap
		}
		nameSt := sFg
		if selected && v.focus == 1 {
			nameSt = sAcc
		}
		restSt := sDim
		if p.Restarts > 0 {
			restSt = sAcc
		}
		line += cell(nameSt, col(p.Name, nameW)) + gap + cell(sDim, col(p.Ready, readyW)) + gap +
			cell(kubeStatusStyle(p), col(p.Status, stW)) + gap + cell(restSt, col(fmt.Sprint(p.Restarts), restW)) + gap +
			cell(sDim, col(kube.Age(p.Created, now), ageW))
		if nodeW > 0 {
			line += gap + cell(sDim, col(p.Node, nodeW))
		}
		out = append(out, zone.Mark(fmt.Sprintf("kpod:%d", i), line))
	}
	return out
}

// ---- mouse da aba ----

func (m *Model) kubeMouse(v *kubeView, msg tea.MouseMsg) (tea.Cmd, bool) {
	cols, rows := m.termSize()
	x, y := msg.X-(m.sideW()+1), msg.Y-sessionOriginY
	if x < -1 || x >= cols || y < 0 || y >= rows {
		return nil, false
	}
	if msg.Action != tea.MouseActionPress {
		return nil, true
	}
	wheel := map[tea.MouseButton]int{tea.MouseButtonWheelUp: -3, tea.MouseButtonWheelDown: 3}
	if d, ok := wheel[msg.Button]; ok {
		if x < m.kubeListW() {
			v.nsOff = max(0, min(len(v.nsRows())-m.kubeListH(), v.nsOff+d))
		} else {
			v.off = max(0, min(len(v.pods)-m.kubePodsH(), v.off+d))
			v.sel = max(v.off, min(v.sel, v.off+m.kubePodsH()-1))
		}
		return nil, true
	}
	if msg.Button != tea.MouseButtonLeft {
		return nil, true
	}
	rowsNS := v.nsRows()
	for i := v.nsOff; i < min(len(rowsNS), v.nsOff+m.kubeListH()); i++ {
		if zone.Get(fmt.Sprintf("kns:%d", i)).InBounds(msg) {
			v.focus, v.nsSel = 0, i
			return m.kubeSetNS(v, rowsNS[i]), true
		}
	}
	for i := v.off; i < min(len(v.pods), v.off+m.kubePodsH()); i++ {
		if zone.Get(fmt.Sprintf("kpod:%d", i)).InBounds(msg) {
			again := v.focus == 1 && v.sel == i && time.Since(v.lastClick) < 500*time.Millisecond
			v.focus, v.sel, v.lastClick = 1, i, time.Now()
			if again { // dois cliques: as ações do pod
				v.lastClick = time.Time{}
				m.modal = newKubePodMenu(v, v.pods[i])
			}
			return nil, true
		}
	}
	return nil, true
}

// onKubeSessionExit: sair do shell (exit) fecha a aba, como no SSH. Logs que
// param (pod reiniciou, foi apagado) e erros deixam a aba aberta com o motivo.
func (m *Model) onKubeSessionExit(i int, s *session.Session, k *kubeSession) {
	code := s.ExitCode()
	switch {
	case k.term && code == 0:
		m.closeTab(i)
		m.say("terminal do cluster "+k.kctx+" encerrado", false)
	case k.term:
		m.say(fmt.Sprintf("terminal do cluster %s encerrado (código %d) · enter abre de novo", k.kctx, code), true)
	case !k.logs && code == 0:
		m.closeTab(i)
		m.say("shell em "+k.pod+" encerrado", false)
	case k.logs:
		m.say("os logs de "+k.pod+" pararam · enter abre de novo", code != 0)
	default:
		m.say(fmt.Sprintf("shell em %s encerrado (código %d) · enter abre de novo", k.pod, code), true)
	}
}
