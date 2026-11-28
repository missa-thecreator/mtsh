package ui

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"

	"mtsh/internal/config"
	"mtsh/internal/dbconn"
	"mtsh/internal/session"
)

// ---- caminho até o banco ----

// openTunnel pede ao ssh mestre (socket ctl) pra encaminhar uma porta local
// pro banco, como o servidor o enxerga. Não loga de novo.
func openTunnel(ctl, target, host string, port int) (addr string, closeFn func(), err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	local := l.Addr().(*net.TCPAddr).Port
	l.Close() // shortcut: outra coisa pode pegar a porta nesse meio-tempo; o ssh aí falha e a pessoa tenta de novo
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	spec := fmt.Sprintf("127.0.0.1:%d:%s:%d", local, host, port)
	if out, err := exec.Command("ssh", "-S", ctl, "-O", "forward", "-L", spec, target).CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("não deu pra abrir o túnel: %s", strings.TrimSpace(string(out)))
	}
	closeFn = func() { exec.Command("ssh", "-S", ctl, "-O", "cancel", "-L", spec, target).Run() }
	return fmt.Sprintf("127.0.0.1:%d", local), closeFn, nil
}

// startMaster abre uma conexão SSH em segundo plano (sem shell) só pra
// carregar o túnel. Sem terminal: entra com a chave ou com a senha salva.
func startMaster(c *exec.Cmd, ctl string) error {
	var stderr bytes.Buffer
	c.Stderr = &stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // sem terminal: o ssh nunca pergunta nada na tela do mtsh
	if err := c.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() { c.Wait(); close(exited) }()
	deadline := time.After(30 * time.Second)
	for {
		if _, err := os.Stat(ctl); err == nil {
			return nil
		}
		select {
		case <-exited:
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "o ssh saiu sem dizer o motivo"
			}
			return fmt.Errorf("não conectou no servidor: %s", msg)
		case <-deadline:
			c.Process.Kill()
			return errors.New("o servidor não respondeu em 30s")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ---- estado ----

type dbConn struct {
	db      *sql.DB
	close   func() // fecha o túnel (e o ssh em segundo plano, se foi aberto pra isso)
	current string // base atual
	items   []dbconn.Item
	listDBs bool // mostrando as bases em vez das tabelas

	addr, pass string // pra reconectar em outra base (o Postgres não troca de base na mesma conexão)
}

func (c *dbConn) shutdown() {
	c.db.Close()
	c.close()
}

// colSpan: a coluna col ocupa as células [x0, x1) da linha da grade.
type colSpan struct{ col, x0, x1 int }

// dbView é uma aba de banco.
type dbView struct {
	cfg        config.Database
	closed     bool
	focus      int // 0 lista, 1 editor, 2 resultado
	sel, off   int // lista
	conn       *dbConn
	connecting bool

	editor    *sqlEditor
	edDrag    bool               // arrastando o mouse no editor (selecionando)
	running   context.CancelFunc // comando em andamento (nil = nenhum)
	runText   string
	exporting *atomic.Int64 // export em andamento: linhas gravadas até agora

	res              *dbconn.Result
	resStmt          string    // a consulta que gerou res (o export roda de novo)
	rRow, rCol, rOff int       // célula destacada e primeira linha à vista
	cOff             int       // primeira coluna à vista
	colSpans         []colSpan // onde cada coluna à vista foi desenhada (pro clique)
	lastClick        time.Time // último clique na grade (dois rápidos = abrir)

	msg    string
	msgErr bool

	savePass *string // senha digitada ao conectar, pra salvar se der certo
}

func newDBView(cfg config.Database) *dbView {
	ed := newSQLEditor("SELECT * FROM …;   (F5 roda o comando onde está o cursor)")
	return &dbView{cfg: cfg, editor: ed}
}

func (v *dbView) say(text string, isErr bool) { v.msg, v.msgErr = text, isErr }

func (v *dbView) close() {
	v.closed = true
	if v.running != nil {
		v.running()
	}
	if v.conn != nil {
		go v.conn.shutdown()
		v.conn = nil
	}
}

func (v *dbView) setFocus(f int) {
	if v.conn == nil {
		f = 0
	}
	v.focus = f
	if f == 1 {
		v.editor.Focus()
	} else {
		v.editor.Blur()
	}
}

// ---- abas de banco (depois das de servidor) ----

func (m *Model) currentDB() *dbView {
	i := m.tab - len(m.sessions) - 1
	if i < 0 || i >= len(m.dbTabs) {
		return nil
	}
	return m.dbTabs[i]
}

func (m *Model) tabCount() int { return len(m.sessions) + len(m.dbTabs) }

func (m *Model) dbTabOpen(name string) bool {
	for _, v := range m.dbTabs {
		if v.cfg.Name == name {
			return true
		}
	}
	return false
}

// openDB abre o banco numa aba (ou vai pra aba dele, se já estiver aberta).
func (m *Model) openDB(d config.Database) tea.Cmd {
	for i, v := range m.dbTabs {
		if v.cfg.Name == d.Name {
			m.tab = len(m.sessions) + i + 1
			return nil
		}
	}
	v := newDBView(d)
	m.dbTabs = append(m.dbTabs, v)
	m.tab = m.tabCount()
	m.toast = ""
	return m.dbConnect(v)
}

func (m *Model) closeDBTab(v *dbView) {
	for i, x := range m.dbTabs {
		if x != v {
			continue
		}
		v.close()
		idx := len(m.sessions) + i + 1
		m.dbTabs = append(m.dbTabs[:i], m.dbTabs[i+1:]...)
		switch {
		case m.tab == idx:
			m.tab = 0
		case m.tab > idx:
			m.tab--
		}
		return
	}
}

// ---- lista à esquerda ----

type dbRow struct {
	label  string
	kind   string // dbs, item
	index  int    // em conn.items
	faint  bool
	accent bool
}

func dbRows(v *dbView) []dbRow {
	if v.conn == nil {
		return nil
	}
	var rows []dbRow
	if !v.conn.listDBs && (v.cfg.Engine == dbconn.Postgres || v.conn.current != "") {
		rows = append(rows, dbRow{label: "‹ bases", kind: "dbs", faint: true})
	}
	for i, it := range v.conn.items {
		label := it.Name
		if it.IsDB {
			label += "/"
		}
		rows = append(rows, dbRow{label: label, kind: "item", index: i, accent: it.IsDB})
	}
	return rows
}

// ---- comandos em segundo plano ----

type dbConnectedMsg struct {
	v    *dbView
	conn *dbConn
	err  error
}

type dbResultMsg struct {
	v      *dbView
	stmt   string // o último comando rodado
	n      int    // quantos comandos rodaram (seleção com vários)
	res    dbconn.Result
	err    error
	reload bool // mudou estrutura ou base: recarrega a lista
}

// dbSwitchedMsg: reconectou em outra base (Postgres).
type dbSwitchedMsg struct {
	v       *dbView
	conn    *dbConn
	db      *sql.DB // nil se não deu
	current string
	items   []dbconn.Item
	err     error
}

// dbSwitchDB abre uma conexão nova na base name, pelo mesmo caminho (direto ou túnel).
func dbSwitchDB(v *dbView, c *dbConn, name string) tea.Cmd {
	cfg := v.cfg
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := dbconn.Open(ctx, cfg.Engine, c.addr, cfg.User, c.pass, name)
		if err != nil {
			return dbSwitchedMsg{v: v, conn: c, err: err}
		}
		cur, items, err := dbconn.Objects(ctx, db, cfg.Engine, false)
		return dbSwitchedMsg{v: v, conn: c, db: db, current: cur, items: items, err: err}
	}
}

type dbObjectsMsg struct {
	v       *dbView
	conn    *dbConn
	current string
	items   []dbconn.Item
	err     error
}

// aliveSession é a aba conectada do servidor, se houver.
func (m *Model) aliveSession(name string) *session.Session {
	for _, s := range m.sessions {
		if done, _ := s.Exited(); s.Name == name && !done {
			return s
		}
	}
	return nil
}

// masterCmd monta o ssh em segundo plano: o mesmo da aba (senha salva via
// askpass, socket de controle), mas sem shell (-N). Sem senha salva, só entra
// com chave/agente (BatchMode): não há terminal pra perguntar nada.
func (m *Model) masterCmd(h config.Host) (*exec.Cmd, string, error) {
	c, ctl, err := m.sshCommand(h)
	if err != nil {
		return nil, "", err
	}
	extra := []string{"-N"}
	if c.Env == nil {
		extra = append(extra, "-o", "BatchMode=yes")
	}
	c.Args = append(append([]string{c.Args[0]}, extra...), c.Args[1:]...)
	return c, ctl, nil
}

// dbConnect abre o caminho (túnel pelo servidor, ou direto) e conecta.
// Se a aba do servidor estiver aberta, o túnel passa pela conexão dela.
// Sem senha salva, pergunta antes (como o mysql -p).
func (m *Model) dbConnect(v *dbView) tea.Cmd {
	key := config.DBVaultKey(v.cfg.Name)
	if !m.vault.Has(key) {
		m.modal = newDBPassModal(v)
		return nil
	}
	pass, _ := m.vault.Get(key)
	return m.dbConnectWith(v, pass)
}

func (m *Model) dbConnectWith(v *dbView, pass string) tea.Cmd {
	cfg := v.cfg
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = dbconn.DefaultPort(cfg.Engine)
	}
	var ctl, target string
	var master *exec.Cmd
	if cfg.Via != "" {
		h, ok := m.hostByName(cfg.Via)
		if !ok {
			v.say("o servidor "+cfg.Via+" não existe mais: edite o banco e escolha outro acesso", true)
			return nil
		}
		target = h.Target()
		if s := m.aliveSession(h.Name); s != nil && m.sftp[s] != nil {
			ctl = m.sftp[s].ctl // reaproveita o login da aba aberta
		} else {
			c, newCtl, err := m.masterCmd(h)
			if err != nil {
				v.say(err.Error(), true)
				return nil
			}
			ctl, master = newCtl, c
		}
	}
	v.connecting = true
	v.say("conectando…", false)
	return func() tea.Msg {
		addr, closeT := net.JoinHostPort(host, strconv.Itoa(port)), func() {}
		if cfg.Via != "" {
			if master != nil {
				if err := startMaster(master, ctl); err != nil {
					if master.Env == nil {
						err = fmt.Errorf("%w (sem senha salva do servidor: salve a senha dele ou abra a aba do servidor antes)", err)
					}
					return dbConnectedMsg{v: v, err: err}
				}
			} else if !waitCtl(ctl, func() bool { return v.closed }) {
				return dbConnectedMsg{v: v, err: errors.New("a aba do servidor ainda não conectou")}
			}
			a, c, err := openTunnel(ctl, target, host, port)
			if err != nil {
				if master != nil {
					master.Process.Kill()
				}
				return dbConnectedMsg{v: v, err: err}
			}
			addr, closeT = a, c
			if master != nil {
				closeT = func() { c(); master.Process.Kill() }
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := dbconn.Open(ctx, cfg.Engine, addr, cfg.User, pass, cfg.DB)
		if err != nil {
			closeT()
			return dbConnectedMsg{v: v, err: err}
		}
		c := &dbConn{db: db, close: closeT, addr: addr, pass: pass}
		// sem base definida, mostra as bases pra escolher (o MySQL já faz isso sozinho)
		c.listDBs = cfg.Engine == dbconn.Postgres && cfg.DB == ""
		c.current, c.items, err = dbconn.Objects(ctx, db, cfg.Engine, c.listDBs)
		return dbConnectedMsg{v: v, conn: c, err: err}
	}
}

func dbReload(v *dbView, c *dbConn) tea.Cmd {
	listDBs, engine := c.listDBs, v.cfg.Engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cur, items, err := dbconn.Objects(ctx, c.db, engine, listDBs)
		return dbObjectsMsg{v, c, cur, items, err}
	}
}

// run roda os comandos em ordem (normalmente um só); ctrl+c cancela. Para no
// primeiro erro e mostra o resultado do último.
func (v *dbView) run(stmts ...string) tea.Cmd {
	if v.conn == nil || len(stmts) == 0 {
		return nil
	}
	if v.running != nil {
		v.say("espere o comando terminar (ctrl+c cancela)", true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.running, v.runText = cancel, strings.Join(stmts, "; ")
	v.say("", false)
	db := v.conn.db
	return func() tea.Msg {
		defer cancel()
		msg := dbResultMsg{v: v}
		for i, st := range stmts {
			res, err := dbconn.Run(ctx, db, st)
			msg.stmt, msg.res, msg.n = st, res, i+1
			msg.reload = msg.reload || err == nil && !res.IsQuery
			if errors.Is(ctx.Err(), context.Canceled) && err != nil {
				err = errors.New("cancelado")
			}
			if err != nil {
				if len(stmts) > 1 {
					err = fmt.Errorf("comando %d de %d: %w", i+1, len(stmts), err)
				}
				msg.err = err
				break
			}
		}
		return msg
	}
}

func (m *Model) onDBMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dbConnectedMsg:
		v := msg.v
		v.connecting = false
		if v.closed {
			if msg.conn != nil {
				go msg.conn.shutdown()
			}
			return nil
		}
		if msg.conn == nil {
			v.savePass = nil
			v.say("não conectou: "+msg.err.Error()+" · r tenta de novo", true)
			return nil
		}
		v.conn, v.sel, v.off = msg.conn, 0, 0
		v.say("conectado"+dbWhere(msg.conn), false)
		if v.savePass != nil { // pediu pra lembrar: só grava depois que a senha funcionou
			if err := m.vault.Set(config.DBVaultKey(v.cfg.Name), *v.savePass); err != nil {
				m.say("conectado, mas a senha não foi salva: "+err.Error(), true)
			}
			v.savePass = nil
		}
		if msg.err != nil {
			v.say("conectado, mas não deu pra listar as tabelas: "+msg.err.Error(), true)
		}
		v.setFocus(1)
	case dbSwitchedMsg:
		v := msg.v
		if v.closed || v.conn != msg.conn {
			if msg.db != nil {
				go msg.db.Close()
			}
			return nil
		}
		if msg.db == nil {
			v.say("não deu pra abrir a base: "+msg.err.Error(), true)
			return nil
		}
		old := v.conn.db
		v.conn.db, v.conn.current, v.conn.items, v.conn.listDBs = msg.db, msg.current, msg.items, false
		go old.Close()
		v.sel, v.off = 0, 0
		v.say("base "+msg.current, false)
		if msg.err != nil {
			v.say("base "+msg.current+", mas não deu pra listar as tabelas: "+msg.err.Error(), true)
		}
	case dbObjectsMsg:
		v := msg.v
		if v.closed || v.conn != msg.conn {
			return nil
		}
		if msg.err != nil {
			v.say("não deu pra listar: "+msg.err.Error(), true)
			return nil
		}
		v.conn.current, v.conn.items = msg.current, msg.items
		v.sel, v.off = max(0, min(v.sel, len(dbRows(v))-1)), 0
	case dbResultMsg:
		v := msg.v
		v.running = nil
		if v.closed {
			return nil
		}
		if msg.err != nil {
			v.say(msg.err.Error(), true)
			if msg.reload && v.conn != nil { // os comandos antes do erro podem ter mudado a estrutura
				return dbReload(v, v.conn)
			}
			return nil
		}
		r := msg.res
		v.res, v.resStmt = &r, ""
		if r.IsQuery {
			v.resStmt = msg.stmt
		}
		v.rRow, v.rCol, v.rOff, v.cOff = 0, 0, 0, 0
		took := r.Took.Round(time.Millisecond)
		switch {
		case r.IsQuery && r.Truncated:
			v.say(fmt.Sprintf("mostrando as %d primeiras linhas · %v", len(r.Rows), took), false)
		case r.IsQuery:
			v.say(fmt.Sprintf("%s · %v", plural(len(r.Rows), "linha", "linhas"), took), false)
		case useTarget(msg.stmt) != "":
			// trocar de base (MySQL/MariaDB): "base loja" diz mais que "0 linhas afetadas"
			v.say("base "+useTarget(msg.stmt), false)
		default:
			v.say(fmt.Sprintf("✓ %s · %v", plural(int(r.Affected), "linha afetada", "linhas afetadas"), took), false)
		}
		if msg.n > 1 {
			v.say(fmt.Sprintf("✓ %d comandos · último: %s", msg.n, v.msg), false)
		}
		if msg.reload && v.conn != nil {
			v.conn.listDBs = false
			return dbReload(v, v.conn)
		}
	}
	return nil
}

func dbWhere(c *dbConn) string {
	if c.current == "" {
		return ""
	}
	return " · base " + c.current
}

// ---- teclado da aba ----

func (m *Model) dbKey(v *dbView, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	switch key {
	case "ctrl+b":
		m.tab = 0
		return nil
	case "ctrl+c":
		if v.running != nil {
			v.running()
		} else if t, _, ok := v.editor.Selection(); ok && v.focus == 1 {
			return copyCmd(t)
		}
		return nil
	case "f5", "ctrl+e":
		return m.dbRunAtCursor(v)
	case "tab", "shift+tab":
		if v.conn != nil {
			d := 1
			if key == "shift+tab" {
				d = 2
			}
			v.setFocus((v.focus + d) % 3)
		}
		return nil
	case "esc":
		if v.focus == 1 && v.editor.HasSelection() {
			v.editor.ClearSelection()
			return nil
		}
		if v.focus != 0 {
			v.setFocus(0)
			return nil
		}
		m.askCloseDB(v)
		return nil
	}
	if v.conn == nil {
		switch key {
		case "r", "enter":
			if !v.connecting {
				return m.dbConnect(v)
			}
		case "x", "ctrl+w":
			m.closeDBTab(v)
		}
		return nil
	}
	// digitou texto na lista ou no resultado: vai pro editor, sem perder a tecla
	if v.focus != 1 && (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && !k.Alt && !dbShortcut(v.focus, key) {
		v.setFocus(1)
		// cursor no fim de um texto que não termina em linha nova (ex: o SELECT de
		// uma tabela aberta): o comando novo começa na linha de baixo
		if val := v.editor.Value(); strings.TrimSpace(val) != "" && !strings.HasSuffix(val, "\n") &&
			v.editor.Offset() == len([]rune(val)) {
			v.editor.InsertString("\n")
		}
	}
	switch v.focus {
	case 1:
		_, edH := m.dbLayout()
		v.editor.Update(k, max(1, edH-2))
		return nil
	case 2:
		return m.dbResultKey(v, key)
	}
	return m.dbListKey(v, key)
}

// dbShortcut são as letras que já fazem algo na lista (0) e no resultado (2).
func dbShortcut(focus int, key string) bool {
	switch focus {
	case 0:
		return key == "j" || key == "k"
	case 2:
		return strings.Contains("hjklgGye", key) && len(key) == 1
	}
	return false
}

func (m *Model) askCloseDB(v *dbView) {
	m.modal = &confirmModal{
		title: "Fechar aba", okLabel: "Fechar", focusOK: true,
		text: "Fechar o banco " + v.cfg.Name + "?\nA conexão é encerrada.",
		onOK: func(m *Model) tea.Cmd { m.closeDBTab(v); return nil },
	}
}

func (m *Model) dbListKey(v *dbView, key string) tea.Cmd {
	rows := dbRows(v)
	n := m.dbListH()
	move := func(d int) {
		v.sel = max(0, min(len(rows)-1, v.sel+d))
		if v.sel < v.off {
			v.off = v.sel
		}
		if v.sel >= v.off+n {
			v.off = v.sel - n + 1
		}
	}
	switch key {
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup":
		move(-n)
	case "pgdown":
		move(n)
	case "home":
		move(-len(rows))
	case "end":
		move(len(rows))
	case "enter":
		return m.dbOpenRow(v)
	case "ctrl+r":
		return dbReload(v, v.conn)
	}
	return nil
}

func (m *Model) dbOpenRow(v *dbView) tea.Cmd {
	rows := dbRows(v)
	if v.sel >= len(rows) {
		return nil
	}
	r := rows[v.sel]
	if r.kind == "dbs" {
		v.conn.listDBs = true
		v.sel = 0
		return dbReload(v, v.conn)
	}
	it := v.conn.items[r.index]
	if it.IsDB {
		if v.cfg.Engine == dbconn.Postgres {
			if v.running != nil {
				v.say("espere o comando terminar (ctrl+c cancela)", true)
				return nil
			}
			v.say("abrindo a base "+it.Name+"…", false)
			return dbSwitchDB(v, v.conn, it.Name)
		}
		return v.run(it.Query) // USE: a lista recarrega com as tabelas
	}
	// abre a tabela: o SELECT vai pro fim do editor, sem apagar o que estava lá
	text := strings.TrimRight(v.editor.Value(), "\n ")
	if text != "" {
		text += "\n"
	}
	v.editor.SetValue(text + it.Query)
	v.editor.CursorEnd()
	return v.run(it.Query)
}

// dbRunAtCursor roda o trecho selecionado no editor (todos os comandos dele) ou,
// sem seleção, o comando (separado por ;) onde está o cursor.
func (m *Model) dbRunAtCursor(v *dbView) tea.Cmd {
	if v.conn == nil {
		return nil
	}
	if t, _, ok := v.editor.Selection(); ok {
		var stmts []string
		for _, st := range dbconn.Split(t) {
			stmts = append(stmts, st.Text)
		}
		if len(stmts) == 0 {
			v.say("a seleção não tem nenhum comando", true)
			return nil
		}
		return v.run(stmts...)
	}
	st, ok := dbconn.At(v.editor.Value(), v.editor.Offset())
	if !ok {
		v.say("o editor está vazio", true)
		return nil
	}
	return v.run(st.Text)
}

func (m *Model) dbResultKey(v *dbView, key string) (cmd tea.Cmd) {
	r := v.res
	if r == nil || len(r.Columns) == 0 {
		return nil
	}
	n := m.dbGridH()
	switch key {
	case "up", "k":
		v.rRow--
	case "down", "j":
		v.rRow++
	case "pgup":
		v.rRow -= n
	case "pgdown":
		v.rRow += n
	case "left", "h":
		v.rCol--
	case "right", "l":
		v.rCol++
	case "home":
		v.rCol = 0
	case "end":
		v.rCol = len(r.Columns) - 1
	case "g":
		v.rRow = 0
	case "G":
		v.rRow = len(r.Rows) - 1
	case "y":
		if len(r.Rows) > 0 {
			cmd = copyCmd(r.Rows[v.rRow][v.rCol].Text)
		}
	case "e":
		if v.resStmt != "" {
			m.modal = newDBExportModal(v)
		}
	case "enter":
		if len(r.Rows) > 0 {
			c := r.Rows[v.rRow][v.rCol]
			m.modal = &valueModal{title: r.Columns[v.rCol], text: c.Text, null: c.Null}
		}
	}
	v.rRow = max(0, min(len(r.Rows)-1, v.rRow))
	v.rCol = max(0, min(len(r.Columns)-1, v.rCol))
	if v.rRow < v.rOff {
		v.rOff = v.rRow
	}
	if v.rRow >= v.rOff+n {
		v.rOff = v.rRow - n + 1
	}
	if v.rCol < v.cOff {
		v.cOff = v.rCol
	}
	return cmd
}

// ---- mouse da aba ----

func (m *Model) dbMouse(v *dbView, msg tea.MouseMsg) (tea.Cmd, bool) {
	cols, rows := m.termSize()
	x, y := msg.X-(m.sideW()+1), msg.Y-sessionOriginY
	// arrastar a borda entre a lista e o editor muda a largura da lista
	// (x conta a partir do começo da lista, então a borda fica em x == listW)
	listW, _ := m.dbLayout()
	switch {
	case m.dbResizing && msg.Action == tea.MouseActionMotion:
		m.cfg.DBListWidth = dbClampListW(x, cols)
		return nil, true
	case m.dbResizing && msg.Action == tea.MouseActionRelease:
		m.dbResizing = false
		m.persist()
		return nil, true
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft &&
		x == listW && y >= 0 && y < rows-2:
		m.dbResizing = true
		m.cfg.DBListWidth = listW
		return nil, true
	}
	if x < -1 || x >= cols || y < 0 || y >= rows {
		return nil, false
	}
	if v.edDrag && msg.Action != tea.MouseActionPress {
		// arrastando no editor: estende a seleção até onde o mouse está
		if msg.Action == tea.MouseActionMotion {
			if ex, ey := zone.Get("db:editor").Pos(msg); ex >= 0 && ey >= 0 {
				v.editor.Click(ex, ey, true)
			}
		}
		if msg.Action == tea.MouseActionRelease {
			v.edDrag = false
		}
		return nil, true
	}
	if msg.Action != tea.MouseActionPress {
		return nil, true
	}
	wheel := map[tea.MouseButton]int{tea.MouseButtonWheelUp: -3, tea.MouseButtonWheelDown: 3}
	if d, ok := wheel[msg.Button]; ok {
		switch {
		case zone.Get("db:list").InBounds(msg):
			v.off = max(0, min(len(dbRows(v))-m.dbListH(), v.off+d))
		case zone.Get("db:editor").InBounds(msg):
			_, edH := m.dbLayout()
			v.editor.Scroll(d, edH-1)
		case zone.Get("db:grid").InBounds(msg) && v.res != nil:
			v.rOff = max(0, min(len(v.res.Rows)-m.dbGridH(), v.rOff+d))
			v.rRow = max(v.rOff, min(v.rRow, v.rOff+m.dbGridH()-1))
		}
		return nil, true
	}
	if msg.Button != tea.MouseButtonLeft {
		return nil, true
	}
	for i := v.off; i < v.off+m.dbListH(); i++ {
		if zone.Get(fmt.Sprintf("db:l:%d", i)).InBounds(msg) {
			again := v.sel == i && v.focus == 0
			v.sel = i
			v.setFocus(0)
			if again { // segundo clique abre
				return m.dbOpenRow(v), true
			}
			return nil, true
		}
	}
	if v.res != nil {
		for i := v.rOff; i < min(len(v.res.Rows), v.rOff+m.dbGridH()); i++ {
			if z := zone.Get(fmt.Sprintf("db:r:%d", i)); z.InBounds(msg) {
				col := v.rCol
				px, _ := z.Pos(msg)
				for _, sp := range v.colSpans {
					if px >= sp.x0 && px < sp.x1 {
						col = sp.col
					}
				}
				// segundo clique na mesma célula, ou dois rápidos na linha (a grade
				// pode ter rolado pra mostrar a célula inteira), mostra o valor inteiro
				quick := time.Since(v.lastClick) < 500*time.Millisecond
				if v.focus == 2 && v.rRow == i && (v.rCol == col || quick) {
					v.lastClick = time.Time{}
					return m.dbResultKey(v, "enter"), true
				}
				v.rRow, v.rCol, v.lastClick = i, col, time.Now()
				v.setFocus(2)
				return nil, true
			}
		}
	}
	if zone.Get("db:editor").InBounds(msg) {
		v.setFocus(1)
		ex, ey := zone.Get("db:editor").Pos(msg)
		v.editor.Click(ex, ey, msg.Shift)
		v.edDrag = true
	}
	return nil, true
}

// ---- cadastro (área principal, linha Bancos do menu) ----

func (m *Model) dbIndex(name string) int {
	for i, d := range m.cfg.Databases {
		if d.Name == name {
			return i
		}
	}
	return -1
}

func (m *Model) askDeleteDB(i int) {
	d := m.cfg.Databases[i]
	m.modal = &confirmModal{
		title: "Apagar banco", okLabel: "Apagar",
		text: "Apagar o cadastro de " + d.Name + "?\nO banco em si não é alterado.",
		onOK: func(m *Model) tea.Cmd {
			j := m.dbIndex(d.Name)
			if j < 0 {
				return nil
			}
			prev := m.cfg.Databases
			m.cfg.Databases = append(append([]config.Database{}, prev[:j]...), prev[j+1:]...)
			if !m.persist() {
				m.cfg.Databases = prev
				return nil
			}
			m.vault.Delete(config.DBVaultKey(d.Name))
			m.say(d.Name+" apagado", false)
			return nil
		},
	}
}

var dbEngines = []string{dbconn.MySQL, dbconn.Postgres}

func engineLabel(e string) string {
	if e == dbconn.Postgres {
		return "PostgreSQL"
	}
	return "MySQL / MariaDB"
}

func engineShort(e string) string {
	if e == dbconn.Postgres {
		return "postgres"
	}
	return "mysql"
}

type dbForm struct {
	orig                             int // índice em Databases; -1 = novo
	name, host, port, user, pass, db textinput.Model
	engine, via                      string
	vias                             []string // "" (direto) + servidores
	focus                            int      // 0 nome, 1 tipo, 2 acesso, 3 host, 4 porta, 5 usuário, 6 senha, 7 base, 8 cancelar, 9 salvar
	err                              string
}

const dbFormFields = 10

func newDBForm(m *Model, i int) *dbForm {
	f := &dbForm{orig: i, engine: dbconn.MySQL, vias: []string{""},
		name: newInput("produção", 34), host: newInput("127.0.0.1", 34), port: newInput("3306", 8),
		user: newInput("root", 34), pass: newInput("(vazio: pergunta ao conectar)", 34), db: newInput("(opcional)", 34)}
	for _, h := range m.cfg.Hosts {
		f.vias = append(f.vias, h.Name)
	}
	f.pass.EchoMode = textinput.EchoPassword
	f.pass.EchoCharacter = '•'
	if i >= 0 {
		d := m.cfg.Databases[i]
		f.engine, f.via = d.Engine, d.Via
		f.name.SetValue(d.Name)
		f.host.SetValue(d.Host)
		if d.Port != 0 {
			f.port.SetValue(strconv.Itoa(d.Port))
		}
		f.user.SetValue(d.User)
		f.db.SetValue(d.DB)
		if m.vault.Has(config.DBVaultKey(d.Name)) {
			f.pass.Placeholder = "(salva; vazio mantém)"
		}
	}
	f.setEngine(f.engine)
	f.setFocus(0)
	return f
}

func (f *dbForm) inputs() []*textinput.Model {
	return []*textinput.Model{&f.name, nil, nil, &f.host, &f.port, &f.user, &f.pass, &f.db}
}

func (f *dbForm) setFocus(i int) {
	f.focus = (i%dbFormFields + dbFormFields) % dbFormFields
	for j, in := range f.inputs() {
		if in == nil {
			continue
		}
		if j == f.focus {
			in.Focus()
		} else {
			in.Blur()
		}
	}
}

func (f *dbForm) setEngine(e string) {
	f.engine = e
	f.port.Placeholder = strconv.Itoa(dbconn.DefaultPort(e))
	f.user.Placeholder = "root"
	if e == dbconn.Postgres {
		f.user.Placeholder = "postgres"
	}
}

func viaLabel(via string) string {
	if via == "" {
		return "Direto (do seu computador)"
	}
	return "via " + via
}

func (f *dbForm) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:close", mm), clicked("m:cancel", mm):
			return nil, true
		case clicked("m:ok", mm):
			return nil, f.save(m)
		case clicked("d:engine", mm):
			f.setFocus(1)
			f.setEngine(cycle(dbEngines, f.engine, 1))
		case clicked("d:via", mm):
			f.setFocus(2)
			f.via = cycle(f.vias, f.via, 1)
		}
		for j := range f.inputs() {
			if clicked(fmt.Sprintf("d:%d", j), mm) {
				f.setFocus(j)
			}
		}
		return nil, false
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		f.err = ""
		switch k.String() {
		case "esc":
			return nil, true
		case "enter":
			if f.focus == 8 {
				return nil, true
			}
			return nil, f.save(m)
		case "tab", "down":
			f.setFocus(f.focus + 1)
			return nil, false
		case "shift+tab", "up":
			f.setFocus(f.focus - 1)
			return nil, false
		case "left", "right":
			d := 1
			if k.String() == "left" {
				d = -1
			}
			switch {
			case f.focus == 1:
				f.setEngine(cycle(dbEngines, f.engine, d))
				return nil, false
			case f.focus == 2:
				f.via = cycle(f.vias, f.via, d)
				return nil, false
			case f.focus >= 8:
				f.setFocus(17 - f.focus)
				return nil, false
			}
		}
	}
	if f.focus < len(f.inputs()) && f.inputs()[f.focus] != nil {
		in := f.inputs()[f.focus]
		var cmd tea.Cmd
		*in, cmd = in.Update(msg)
		return cmd, false
	}
	return nil, false
}

func (f *dbForm) save(m *Model) bool {
	name := strings.TrimSpace(f.name.Value())
	if name == "" {
		f.err = "Dê um nome pro banco."
		f.setFocus(0)
		return false
	}
	port := 0
	if p := strings.TrimSpace(f.port.Value()); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			f.err = "Porta inválida."
			f.setFocus(4)
			return false
		}
		port = n
	}
	dbs := append([]config.Database{}, m.cfg.Databases...)
	for j, d := range dbs {
		if d.Name == name && j != f.orig {
			f.err = "Já existe um banco com esse nome."
			f.setFocus(0)
			return false
		}
	}
	user := strings.TrimSpace(f.user.Value())
	if user == "" {
		user = f.user.Placeholder
	}
	d := config.Database{Name: name, Via: f.via, Engine: f.engine, Host: strings.TrimSpace(f.host.Value()), Port: port,
		User: user, DB: strings.TrimSpace(f.db.Value())}
	oldName := ""
	if f.orig >= 0 {
		oldName = dbs[f.orig].Name
		dbs[f.orig] = d
	} else {
		dbs = append(dbs, d)
	}
	prev := m.cfg.Databases
	m.cfg.Databases = dbs
	if !m.persist() {
		m.cfg.Databases = prev
		f.err = "Não deu pra salvar a config."
		return false
	}
	if oldName != "" && oldName != name {
		m.vault.Rename(config.DBVaultKey(oldName), config.DBVaultKey(name))
	}
	if p := f.pass.Value(); p != "" {
		if err := m.vault.Set(config.DBVaultKey(name), p); err != nil {
			f.err = "Banco salvo, mas a senha não: " + err.Error()
			return false
		}
	}
	m.cardSel = m.dbIndex(name)
	m.say("✓ "+name+" salvo · enter conecta", false)
	return true
}

func (f *dbForm) view(m *Model) string {
	in := func(j int, ti textinput.Model) string {
		return zone.Mark(fmt.Sprintf("d:%d", j), fieldBox(ti.View(), ti.Width))
	}
	hostHint := "como o servidor enxerga o banco (vazio = 127.0.0.1)"
	if f.via == "" {
		hostHint = "endereço do banco visto do seu computador"
	}
	rows := []string{
		fieldRow("Nome", f.focus == 0, in(0, f.name)),
		fieldRow("Tipo", f.focus == 1, zone.Mark("d:engine", fieldBox(selectLabel(engineLabel(f.engine), 34), 34))),
		fieldRow("Acesso", f.focus == 2, zone.Mark("d:via", fieldBox(selectLabel(runewidth.Truncate(viaLabel(f.via), 30, "…"), 34), 34))),
		sFaint.Render("             via servidor: túnel SSH por ele, sem abrir porta"),
		fieldRow("Host", f.focus == 3, in(3, f.host)),
		sFaint.Render("             " + hostHint),
		fieldRow("Porta", f.focus == 4, in(4, f.port)),
		fieldRow("Usuário", f.focus == 5, in(5, f.user)),
		fieldRow("Senha", f.focus == 6, in(6, f.pass)),
		fieldRow("Base", f.focus == 7, in(7, f.db)),
		"",
	}
	if f.err != "" {
		rows = append(rows, sErr.Render("  "+f.err))
	} else {
		rows = append(rows, sFaint.Render("  ◂ ▸ troca o tipo e o acesso · tab próximo campo · enter salva"))
	}
	btns := button("m:cancel", " Cancelar ", false, f.focus == 8) + "  " + button("m:ok", " Salvar ", true, f.focus == 9)
	rows = append(rows, "", lipgloss.PlaceHorizontal(58, lipgloss.Right, btns), "")
	title := "Novo banco"
	if f.orig >= 0 {
		title = "Editar banco"
	}
	return dialog(title, 64, strings.Join(rows, "\n"))
}

// valueModal mostra o valor inteiro de uma célula (texto longo, JSON…).
type valueModal struct {
	title, text string
	null        bool
	off         int
	wide        bool // texto longo (describe): usa quase a tela toda
}

// size é a largura do texto e quantas linhas aparecem de cada vez.
func (vm *valueModal) size(m *Model) (w, h int) {
	if !vm.wide {
		return 70, 20
	}
	return max(70, min(m.mainW()-10, 130)), max(10, m.bodyH()-12)
}

func (vm *valueModal) lines(w int) []string {
	return strings.Split(lipgloss.NewStyle().Width(w).Render(strings.ReplaceAll(vm.text, "\t", "    ")), "\n")
}

func (vm *valueModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		if clicked("m:close", mm) || clicked("m:ok", mm) {
			return nil, true
		}
		return nil, false
	}
	w, h := vm.size(m)
	last := max(0, len(vm.lines(w))-h)
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Action == tea.MouseActionPress {
		switch mm.Button {
		case tea.MouseButtonWheelUp:
			vm.off = max(0, vm.off-3)
		case tea.MouseButtonWheelDown:
			vm.off = min(last, vm.off+3)
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "esc", "enter", "q":
		return nil, true
	case "y":
		return copyCmd(vm.text), true
	case "down", "j":
		vm.off = min(vm.off+1, last)
	case "up", "k":
		vm.off = max(0, vm.off-1)
	case "pgdown", " ":
		vm.off = min(vm.off+h, last)
	case "pgup":
		vm.off = max(0, vm.off-h)
	case "home", "g":
		vm.off = 0
	case "end", "G":
		vm.off = last
	}
	return nil, false
}

func (vm *valueModal) view(m *Model) string {
	w, h := vm.size(m)
	body := sFaint.Italic(true).Render("NULL")
	if !vm.null {
		ls := vm.lines(w)
		end := min(len(ls), vm.off+h)
		body = sFg.Render(strings.Join(ls[vm.off:end], "\n"))
		if len(ls) > h {
			body += "\n" + sFaint.Render(fmt.Sprintf("linhas %d–%d de %d · ↑↓ pgup/pgdn rolam", vm.off+1, end, len(ls)))
		}
	}
	hint := sFaint.Render("y copia · esc fecha")
	btn := button("m:ok", " Fechar ", true, true)
	return dialog(vm.title, w+8, body+"\n\n"+hint+"\n"+lipgloss.PlaceHorizontal(w+2, lipgloss.Right, btn)+"\n")
}

// ---- cards dos bancos (área principal) ----

// dbGlyph marca banco na barra lateral, nos cards e nas abas.
const dbGlyph = "▤"

func (m Model) viewDBMain() string {
	w := m.mainW()
	title := sBold.Render("Bancos") + sDim.Render("   "+plural(len(m.cfg.Databases), "banco", "bancos"))
	btn := zone.Mark("act:newdb", sBtnMain.Render(" + Banco "))
	header := title + strings.Repeat(" ", max(1, w-lipgloss.Width(title)-lipgloss.Width(btn))) + btn
	var grid string
	if len(m.cfg.Databases) == 0 {
		grid = lipgloss.NewStyle().Width(min(w, 70)).Render(
			sBold.Render("Nenhum banco ainda.") + "\n\n" +
				sDim.Render("Cadastre um em ") + sAcc.Render("+ Banco") + sDim.Render(" (ou a). Dá pra chegar nele por um servidor já cadastrado ") +
				sDim.Render("(túnel SSH, sem abrir porta) ou direto, se o seu computador já alcança o banco.") + "\n\n" +
				sDim.Render("MySQL, MariaDB e PostgreSQL."))
	} else {
		cols, cw := m.cols(), m.cardW()
		var lines []string
		first := m.gridOff * cols
		last := min(len(m.cfg.Databases), first+m.gridRows()*cols)
		for start := first; start < last; start += cols {
			var row []string
			for i := start; i < min(start+cols, last); i++ {
				if i > start {
					row = append(row, " ")
				}
				row = append(row, zone.Mark(fmt.Sprintf("dbcard:%d", i), m.viewDBCard(m.cfg.Databases[i], cw, i == m.cardSel)))
			}
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
		}
		grid = strings.Join(lines, "\n")
	}
	return lipgloss.NewStyle().PaddingLeft(2).Width(w + 2).Height(m.bodyH()).MaxHeight(m.bodyH()).
		Render(header + "\n\n" + grid)
}

func (m Model) viewDBCard(d config.Database, w int, sel bool) string {
	inner := w - 4
	focused := sel && m.focus == focusGrid
	nameStyle := sBold
	if focused {
		nameStyle = nameStyle.Foreground(cAcc)
	}
	name := sAcc.Render(dbGlyph) + " " + nameStyle.Render(runewidth.Truncate(d.Name, inner-2, "…"))
	if m.dbTabOpen(d.Name) {
		if gap := inner - lipgloss.Width(name) - lipgloss.Width("● aberto"); gap > 0 {
			name += strings.Repeat(" ", gap) + sOk.Render("● aberto")
		}
	}
	host := d.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if d.Port != 0 {
		host += fmt.Sprintf(":%d", d.Port)
	}
	who := engineShort(d.Engine) + " · " + d.User + "@" + host
	if d.DB != "" {
		who += "/" + d.DB
	}
	via := sFaint.Render("direto")
	if d.Via != "" {
		via = sFaint.Render("via ") + sDim.Render(runewidth.Truncate(d.Via, inner-4, "…"))
	}
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
	return lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).Padding(0, 1).Width(w - 2).
		Render(strings.Join([]string{name, sDim.Render(runewidth.Truncate(who, inner, "…")), via, buttons}, "\n"))
}

// dbGridKey trata o teclado nos cards de banco.
func (m *Model) dbGridKey(k string) tea.Cmd {
	n, cols := len(m.cfg.Databases), m.cols()
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
			return m.openDB(m.cfg.Databases[m.cardSel])
		}
	}
	return nil
}

// ---- desenho da aba ----

func (m *Model) dbLayout() (listW, edH int) {
	cols, rows := m.termSize()
	listW = max(18, min(32, cols/4))
	if w := m.cfg.DBListWidth; w > 0 { // escolhida arrastando a borda
		listW = dbClampListW(w, cols)
	}
	return listW, max(3, min(12, (rows-2)/3))
}

// dbClampListW: a lista tem um mínimo, e o editor/resultado ficam com pelo menos 40 colunas.
func dbClampListW(w, cols int) int { return max(12, min(w, cols-1-40)) }

// dbListH é quantas linhas a lista mostra (tudo menos o título).
func (m *Model) dbListH() int {
	_, rows := m.termSize()
	return max(1, rows-2-1)
}

// dbGridH é quantas linhas de dados cabem na grade.
func (m *Model) dbGridH() int {
	_, rows := m.termSize()
	_, edH := m.dbLayout()
	return max(1, rows-2-edH-2-1) // status+ajuda, editor, linha do resultado, cabeçalho das colunas
}

func (m Model) viewDBTab(v *dbView) string {
	cols, rows := m.termSize()
	return lipgloss.NewStyle().PaddingLeft(1).Render(m.dbHeader(v, cols) + "\n" + strings.Join(m.viewDB(v, cols, rows), "\n"))
}

// dbHeader é a barra de título da aba: banco, tipo, base e por onde passa.
func (m Model) dbHeader(v *dbView, cols int) string {
	bg := lipgloss.NewStyle().Background(cSel)
	d := v.cfg
	parts := []string{
		bg.Foreground(cAcc).Render(" "+dbGlyph) + bg.Foreground(cAcc).Bold(true).Render(" "+d.Name),
		bg.Foreground(cFg).Render(engineLabel(d.Engine)),
	}
	if d.Via != "" {
		parts = append(parts, bg.Foreground(cDim).Render("via "+d.Via))
	} else {
		parts = append(parts, bg.Foreground(cDim).Render("direto"))
	}
	if v.conn != nil && (v.conn.listDBs || (v.cfg.Engine == dbconn.MySQL && v.conn.current == "")) {
		// ainda escolhendo (no Postgres a conexão já está numa base, mas não é a escolhida)
		parts = append(parts, bg.Foreground(cAcc).Render("escolha uma base"))
	} else if v.conn != nil && v.conn.current != "" {
		parts = append(parts, bg.Foreground(cFg).Render("base "+v.conn.current))
	}
	if v.conn == nil && !v.connecting {
		parts = append(parts, bg.Foreground(cErr).Render("desconectado"))
	}
	line := strings.Join(parts, bg.Foreground(cFaint).Render("  ·  "))
	line = lipgloss.NewStyle().MaxWidth(cols).Render(line)
	if w := lipgloss.Width(line); w < cols {
		line += bg.Render(strings.Repeat(" ", cols-w))
	}
	return line
}

func (m Model) viewDB(v *dbView, cols, rows int) []string {
	listW, edH := m.dbLayout()
	rightW := cols - listW - 1
	top := rows - 2
	left := m.viewDBList(v, listW, top)

	var right []string
	switch {
	case v.connecting:
		right = []string{"", sDim.Render("  conectando…")}
	case v.conn == nil:
		right = []string{"", sDim.Render("  sem conexão · r ou enter tenta de novo · x fecha a aba")}
	default:
		edTitle := lipgloss.NewStyle().Foreground(cDim).Bold(true)
		if v.focus == 1 {
			edTitle = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
		}
		hint := "  F5 roda o comando onde está o cursor · shift+setas selecionam"
		if v.editor.HasSelection() {
			hint = "  F5 roda a seleção · ctrl+c copia · esc desfaz"
		}
		right = append(right, edTitle.Render(" Editor SQL ")+sFaint.Render(hint))
		// a primeira linha é o título
		ed := v.editor.View(rightW, max(1, edH-1))
		right = append(right, strings.Split(zone.Mark("db:editor", strings.Join(ed, "\n")), "\n")...)
		for len(right) < edH {
			right = append(right, "")
		}
		right = append(right, m.viewDBResults(v, rightW, top-edH)...)
	}
	sep := sFaint.Render("│")
	if m.dbResizing {
		sep = sAcc.Render("│")
	}
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
	if v.exporting != nil {
		status = sAcc.Render(fmt.Sprintf(" exportando… %s ", plural(int(v.exporting.Load()), "linha", "linhas"))) +
			sFaint.Render("ctrl+c cancela · "+v.runText)
	} else if v.running != nil {
		status = sAcc.Render(" rodando… ") + sFaint.Render("ctrl+c cancela · "+oneLine(v.runText))
	} else if v.connecting {
		status = sAcc.Render(" conectando em " + v.cfg.Name + "…")
	}
	out = append(out, lipgloss.NewStyle().MaxWidth(cols).Render(status))
	help := " F5 roda o comando do cursor (ou a seleção) · tab troca · enter (ou 2 cliques) abre a tabela ou vê o valor inteiro · y copia · e exporta · esc volta"
	out = append(out, sFaint.MaxWidth(cols).Render(help))
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (m Model) viewDBList(v *dbView, w, h int) []string {
	title := " Tabelas"
	if v.conn != nil && (v.conn.listDBs || (v.cfg.Engine == dbconn.MySQL && v.conn.current == "")) {
		title = " Bases"
	}
	st := lipgloss.NewStyle().Foreground(cDim).Bold(true)
	if v.focus == 0 {
		st = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
	}
	lines := []string{st.MaxWidth(w).Render(title)}
	rows := dbRows(v)
	var body []string
	for i := v.off; i < min(len(rows), v.off+h-1); i++ {
		r := rows[i]
		ls := sFg
		switch {
		case r.faint:
			ls = sDim
		case r.accent:
			ls = sAcc
		}
		line := ls.Render(" " + r.label)
		if i == v.sel {
			hl := lipgloss.NewStyle().Background(cSel).Foreground(cFg)
			if v.focus == 0 {
				hl = hl.Foreground(cAcc).Bold(true)
			}
			line = hl.Width(w).MaxWidth(w).Render(" " + r.label)
		}
		body = append(body, zone.Mark(fmt.Sprintf("db:l:%d", i), lipgloss.NewStyle().MaxWidth(w).Render(line)))
	}
	for len(body) < h-1 {
		body = append(body, "")
	}
	marked := strings.Split(zone.Mark("db:list", strings.Join(body, "\n")), "\n")
	return append(lines, marked...)
}

func (m Model) viewDBResults(v *dbView, w, h int) []string {
	head := lipgloss.NewStyle().Foreground(cDim).Bold(true)
	if v.focus == 2 {
		head = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true)
	}
	r := v.res
	if r == nil {
		return []string{head.Render(" Resultado "), sFaint.Render(" nada ainda: escreva um comando e aperte F5")}
	}
	if !r.IsQuery || len(r.Columns) == 0 {
		return []string{head.Render(" Resultado "), sOk.Render(" ✓ " + plural(int(r.Affected), "linha afetada", "linhas afetadas"))}
	}
	info := fmt.Sprintf(" Resultado · %s × %s ", plural(len(r.Rows), "linha", "linhas"), plural(len(r.Columns), "coluna", "colunas"))
	out := []string{head.Render(info)}

	// largura de cada coluna: o maior entre o nome e os valores (amostra), até 40
	widths := make([]int, len(r.Columns))
	for c, name := range r.Columns {
		widths[c] = max(4, lipgloss.Width(name))
		for i := 0; i < min(len(r.Rows), 300); i++ {
			widths[c] = max(widths[c], lipgloss.Width(cellText(r.Rows[i][c].Text)))
		}
		widths[c] = min(widths[c], 40)
	}
	// espaço das setas: ‹ à esquerda (rolou) e › à direita (tem mais)
	const lead, tail = 1, 2
	room := w - lead - tail
	// garante a coluna destacada inteira à vista, andando cOff pra direita se preciso
	fits := func(from, to int) bool {
		used := 0
		for c := from; c <= to; c++ {
			used += widths[c] + 3
		}
		return used <= room
	}
	for v.cOff < v.rCol && !fits(v.cOff, v.rCol) {
		v.cOff++
	}
	var visible []int
	used := 0
	for c := v.cOff; c < len(r.Columns) && used+widths[c]+3 <= room; c++ {
		visible = append(visible, c)
		used += widths[c] + 3
	}
	if len(visible) == 0 && v.cOff < len(r.Columns) {
		visible = []int{v.cOff}
		widths[v.cOff] = max(1, room-3)
		used = room
	}
	// a próxima coluna não cabe inteira: aparece cortada no que sobrou, em vez
	// de deixar o espaço vazio (fica inteira quando a seleção chega nela)
	if next := v.cOff + len(visible); next < len(r.Columns) && room-used >= 3+4 {
		widths[next] = room - used - 3
		visible = append(visible, next)
	}
	cell := func(text string, wd int) string {
		text = runewidth.Truncate(cellText(text), wd, "…")
		return " " + text + strings.Repeat(" ", max(0, wd-lipgloss.Width(text))) + " "
	}
	// a coluna da esquerda é sempre reservada: com ou sem ‹, cabeçalho e linhas alinham
	left := " "
	if v.cOff > 0 {
		left = "‹"
	}
	hdr := sAcc.Render(left)
	v.colSpans = v.colSpans[:0]
	x := lipgloss.Width(left)
	for _, c := range visible {
		v.colSpans = append(v.colSpans, colSpan{c, x, x + widths[c] + 3})
		x += widths[c] + 3
	}
	for _, c := range visible {
		hdr += sBold.Render(cell(r.Columns[c], widths[c])) + sFaint.Render("│")
	}
	if len(visible) > 0 && visible[len(visible)-1] < len(r.Columns)-1 {
		hdr += sAcc.Render(" ›") // tem mais colunas à direita (→ anda)
	}
	out = append(out, hdr)
	if len(r.Rows) == 0 {
		return append(out, sFaint.Render(" (nenhuma linha)"))
	}
	n := h - 2
	var body []string
	for i := v.rOff; i < min(len(r.Rows), v.rOff+n); i++ {
		line := " "
		for _, c := range visible {
			val := r.Rows[i][c]
			st := sFg
			if val.Null {
				st = sFaint.Italic(true)
			}
			switch {
			case i == v.rRow && c == v.rCol && v.focus == 2:
				st = st.Background(cAcc).Foreground(cDark).Bold(true)
			case i == v.rRow:
				st = st.Background(cSel)
			}
			line += st.Render(cell(val.Text, widths[c])) + sFaint.Render("│")
		}
		body = append(body, zone.Mark(fmt.Sprintf("db:r:%d", i), line))
	}
	return append(out, strings.Split(zone.Mark("db:grid", strings.Join(body, "\n")), "\n")...)
}

// cellText deixa o valor numa linha só, pra caber na grade.
func cellText(s string) string {
	return strings.NewReplacer("\r\n", "↵", "\n", "↵", "\t", " ").Replace(s)
}

// dbCardClick: clique num card de banco (botões Conectar/Editar, ou dois cliques).
func (m *Model) dbCardClick(msg tea.MouseMsg) tea.Cmd {
	for i, d := range m.cfg.Databases {
		z := zone.Get(fmt.Sprintf("dbcard:%d", i))
		if !z.InBounds(msg) {
			continue
		}
		m.focus = focusGrid
		m.cardSel = i
		// linha dos botões: borda(1) + 3 linhas de conteúdo
		if x, y := z.Pos(msg); y == 4 {
			if x >= 2 && x < 2+len(btnConnect) {
				return m.openDB(d)
			}
			if x >= 3+len(btnConnect) && x < 3+len(btnConnect)+len(btnEdit) {
				m.modal = newDBForm(m, i)
				return nil
			}
		}
		id := fmt.Sprintf("dbcard:%d", i)
		if m.lastClickID == id && time.Since(m.lastClickAt) < 400*time.Millisecond {
			m.lastClickID = ""
			return m.openDB(d)
		}
		m.lastClickID, m.lastClickAt = id, time.Now()
		return nil
	}
	return nil
}

// dbPassModal pede a senha do banco ao conectar, quando não está salva.
type dbPassModal struct {
	v        *dbView
	in       textinput.Model
	remember bool
	focus    int // 0 senha, 1 lembrar
}

func newDBPassModal(v *dbView) *dbPassModal {
	p := &dbPassModal{v: v, in: newInput("", 34)}
	p.in.EchoMode = textinput.EchoPassword
	p.in.EchoCharacter = '•'
	p.in.Focus()
	v.say("esperando a senha…", false)
	return p
}

func (p *dbPassModal) submit(m *Model) tea.Cmd {
	pass := p.in.Value()
	if p.remember {
		p.v.savePass = &pass
	}
	return m.dbConnectWith(p.v, pass)
}

func (p *dbPassModal) cancel() {
	p.v.say("sem senha, não conectou · r tenta de novo · x fecha a aba", true)
}

func (p *dbPassModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:ok", mm):
			return p.submit(m), true
		case clicked("m:cancel", mm), clicked("m:close", mm):
			p.cancel()
			return nil, true
		case clicked("d:remember", mm):
			p.remember = !p.remember
		}
		return nil, false
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			p.cancel()
			return nil, true
		case "enter":
			return p.submit(m), true
		case "tab", "shift+tab", "up", "down":
			p.focus = 1 - p.focus
			if p.focus == 0 {
				p.in.Focus()
			} else {
				p.in.Blur()
			}
			return nil, false
		case " ":
			if p.focus == 1 {
				p.remember = !p.remember
				return nil, false
			}
		}
	}
	if p.focus != 0 {
		return nil, false
	}
	var cmd tea.Cmd
	p.in, cmd = p.in.Update(msg)
	return cmd, false
}

func (p *dbPassModal) view(m *Model) string {
	d := p.v.cfg
	where := d.Host
	if where == "" {
		where = "127.0.0.1"
	}
	if d.Via != "" {
		where += " (via " + d.Via + ")"
	}
	box := "☐"
	if p.remember {
		box = "☑"
	}
	rem := lipgloss.NewStyle().Foreground(cDim).Render(box + " Lembrar a senha neste computador")
	if p.focus == 1 {
		rem = sAcc.Render(box + " Lembrar a senha neste computador")
	}
	rows := []string{
		sDim.Render("  " + runewidth.Truncate(d.User+"@"+where, 44, "…")),
		"",
		fieldRow("Senha", p.focus == 0, fieldBox(p.in.View(), p.in.Width)),
		"",
		"             " + zone.Mark("d:remember", rem),
		"",
		sFaint.Render("  enter conecta · tab marca lembrar · esc cancela"),
	}
	btns := button("m:cancel", " Cancelar ", false, false) + "  " + button("m:ok", " Conectar ", true, true)
	rows = append(rows, "", lipgloss.PlaceHorizontal(50, lipgloss.Right, btns), "")
	return dialog("Senha de "+d.Name, 56, strings.Join(rows, "\n"))
}

// useTarget devolve a base de um comando "USE base" ("" se não for USE).
func useTarget(stmt string) string {
	f := strings.Fields(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(stmt), ";")))
	if len(f) != 2 || !strings.EqualFold(f[0], "use") {
		return ""
	}
	return strings.Trim(f[1], "`\"")
}
