package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/config"
	"mtsh/internal/dbconn"
	"mtsh/internal/session"
	"mtsh/internal/vault"
)

func dbTestModel(t *testing.T) *Model {
	d := t.TempDir()
	vt, err := vault.Open(filepath.Join(d, "passwords.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Model{cfgPath: filepath.Join(d, "hosts.yaml"), cfg: &config.File{Hosts: []config.Host{{Name: "srv", Host: "10.0.0.1"}}},
		vault: vt, width: 120, height: 40, open: map[string]bool{}, sftp: map[*session.Session]*sftpView{}}
}

func TestDBFormSalvaRenomeiaApaga(t *testing.T) {
	m := dbTestModel(t)
	f := newDBForm(m, -1)
	f.setEngine(dbconn.Postgres)
	f.via = "srv"
	f.name.SetValue("relatórios")
	f.pass.SetValue("segredo")
	if !f.save(m) {
		t.Fatalf("salvar: %s", f.err)
	}
	d := m.cfg.Databases
	if len(d) != 1 || d[0].Engine != dbconn.Postgres || d[0].User != "postgres" || d[0].Via != "srv" {
		t.Fatalf("salvo: %+v", d)
	}
	if p, _ := m.vault.Get(config.DBVaultKey("relatórios")); p != "segredo" {
		t.Fatal("senha não foi pro cofre")
	}
	// nome repetido é recusado
	f2 := newDBForm(m, -1)
	f2.name.SetValue("relatórios")
	if f2.save(m) {
		t.Fatal("aceitou nome repetido")
	}
	// renomear leva a senha junto; senha vazia mantém a salva
	f3 := newDBForm(m, 0)
	f3.name.SetValue("bi")
	if !f3.save(m) {
		t.Fatal(f3.err)
	}
	if p, _ := m.vault.Get(config.DBVaultKey("bi")); p != "segredo" || m.vault.Has(config.DBVaultKey("relatórios")) {
		t.Fatal("senha não acompanhou o novo nome")
	}
	// apagar (d nos cards + confirmar)
	m.dbMode, m.focus, m.cardSel = true, focusGrid, 0
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m.modal.(*confirmModal).onOK(m)
	if len(m.cfg.Databases) != 0 || m.vault.Has(config.DBVaultKey("bi")) {
		t.Fatal("apagar devia tirar o banco e a senha")
	}
	// tudo foi gravado no arquivo
	saved, err := config.Load(m.cfgPath)
	if err != nil || len(saved.Databases) != 0 {
		t.Fatalf("arquivo: %v %+v", err, saved.Databases)
	}
}

func TestDBRenomearServidorAtualizaAcesso(t *testing.T) {
	m := dbTestModel(t)
	m.cfg.Databases = []config.Database{{Name: "loja", Engine: dbconn.MySQL, Via: "srv"}, {Name: "outro", Via: "x"}}
	h := newHostModal(m, &m.cfg.Hosts[0], "")
	h.name.SetValue("srv2")
	if _, done := h.submit(m); !done {
		t.Fatalf("salvar servidor: %s", h.err)
	}
	if m.cfg.Databases[0].Via != "srv2" || m.cfg.Databases[1].Via != "x" {
		t.Fatalf("acesso não acompanhou: %+v", m.cfg.Databases)
	}
}

func TestMenuBancos(t *testing.T) {
	m := dbTestModel(t)
	rows := m.treeRows()
	if rows[2].kind != rowDBs {
		t.Fatalf("Bancos devia ser a 3ª linha do menu")
	}
	m.focus, m.sideSel = focusSide, 2
	m.activateSide(true)
	if !m.dbMode || !strings.Contains(m.viewMain(), "Nenhum banco ainda") {
		t.Fatal("Bancos devia mostrar a tela de bancos")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if _, ok := m.modal.(*dbForm); !ok {
		t.Fatal("a em Bancos devia abrir o cadastro de banco")
	}
	m.modal = nil
	m.sideSel = 1 // Todos
	m.activateSide(false)
	if m.dbMode {
		t.Fatal("Todos devia voltar pros servidores")
	}
}

func TestAbasDeBanco(t *testing.T) {
	m := dbTestModel(t)
	// direto pra uma porta fechada: abre a aba e falha a conexão, sem travar
	d := config.Database{Name: "loja", Engine: dbconn.MySQL, Host: "127.0.0.1", Port: 1}
	if m.openDB(d) != nil || m.tab != 1 || m.currentDB() == nil || m.current() != nil {
		t.Fatalf("tab=%d", m.tab)
	}
	// sem senha salva: pergunta antes, como o mysql -p
	pm, ok := m.modal.(*dbPassModal)
	if !ok {
		t.Fatalf("devia pedir a senha, modal = %T", m.modal)
	}
	cmd, done := pm.update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !done || cmd == nil {
		t.Fatal("enter devia conectar")
	}
	m.modal = nil
	m.onDBMsg(cmd())
	v := m.currentDB()
	if v.conn != nil || !v.msgErr || v.connecting {
		t.Fatalf("devia ter falhado: %q", v.msg)
	}
	if !strings.Contains(m.View(), "loja") {
		t.Fatal("aba de banco não aparece")
	}
	// abrir de novo só vai pra aba
	m.tab = 0
	if m.openDB(d) != nil || m.tab != 1 || len(m.dbTabs) != 1 {
		t.Fatal("abrir de novo devia só ir pra aba")
	}
	// esc com foco na lista pergunta se fecha; x fecha direto quando desconectado
	m.dbKey(v, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if len(m.dbTabs) != 0 || m.tab != 0 {
		t.Fatalf("x devia fechar a aba: tabs=%d tab=%d", len(m.dbTabs), m.tab)
	}
}

func TestMasterCmd(t *testing.T) {
	m := dbTestModel(t)
	h := config.Host{Name: "k", Host: "10.0.0.9", User: "u", Auth: config.AuthKey, Key: "/tmp/id"}
	c, ctl, err := m.masterCmd(h)
	if err != nil {
		t.Fatal(err)
	}
	args := c.Args[1:]
	if args[0] != "-N" || !slices.Contains(args, "BatchMode=yes") || !slices.Contains(args, "ControlPath="+ctl) || args[len(args)-1] != "u@10.0.0.9" {
		t.Fatalf("args = %q", args)
	}
	// com senha salva: entra pela senha (askpass), sem BatchMode
	m.vault.Set("k", "x")
	h.Auth = config.AuthPassword
	c, _, _ = m.masterCmd(h)
	if slices.Contains(c.Args, "BatchMode=yes") || c.Env == nil {
		t.Fatalf("com senha salva: %q", c.Args)
	}
}

func TestStartMasterFalha(t *testing.T) {
	ctl := filepath.Join(t.TempDir(), "ctl")
	err := startMaster(exec.Command("sh", "-c", "echo 'Permission denied (publickey).' >&2; exit 255"), ctl)
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("erro do ssh devia aparecer: %v", err)
	}
	// o socket aparece: conectou
	err = startMaster(exec.Command("sh", "-c", "sleep 0.3; touch "+ctl+"; sleep 5"), ctl)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(ctl)
}

func TestEditorOffset(t *testing.T) {
	v := newDBView(config.Database{})
	v.editor.Focus()
	v.editor.SetValue("SELECT 1;\nSELECT 2;")
	v.editor.CursorUp() // fica na 1ª linha
	st, _ := dbconn.At(v.editor.Value(), editorOffset(v.editor))
	if st.Text != "SELECT 1" {
		t.Fatalf("1ª linha escolheu %q", st.Text)
	}
	v.editor.CursorDown()
	if st, _ = dbconn.At(v.editor.Value(), editorOffset(v.editor)); st.Text != "SELECT 2" {
		t.Fatalf("2ª linha escolheu %q", st.Text)
	}
}

func TestDBEsc(t *testing.T) {
	m := dbTestModel(t)
	v := newDBView(config.Database{Name: "x"})
	v.conn = &dbConn{}
	v.setFocus(1)
	m.dbKey(v, tea.KeyMsg{Type: tea.KeyEsc}) // editor → lista
	if v.focus != 0 || m.modal != nil {
		t.Fatal("esc no editor devia ir pra lista")
	}
	m.dbKey(v, tea.KeyMsg{Type: tea.KeyEsc}) // lista → pergunta se fecha
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatal("esc na lista devia perguntar se fecha a aba")
	}
}

// TestE2EBancoPeloMenu: o fluxo inteiro, do jeito que a pessoa usa: banco
// direto e banco via servidor com a aba do servidor aberta (reaproveita o
// socket dela). Precisa dos contêineres de MTSH_DB_TEST / MTSH_E2E_SSH_KEY.
func TestE2EBancoPeloMenu(t *testing.T) {
	if os.Getenv("MTSH_DB_TEST") == "" {
		t.Skip("MTSH_DB_TEST não definido")
	}
	m := dbTestModel(t)
	m.vault.Set(config.DBVaultKey("direto"), "s3nha")
	cmd := m.openDB(config.Database{Name: "direto", Engine: dbconn.Postgres, Host: "127.0.0.1", Port: 15432, User: "postgres", DB: "loja"})
	m.onDBMsg(cmd())
	v := m.currentDB()
	if v.conn == nil {
		t.Fatalf("direto não conectou: %s", v.msg)
	}
	v.editor.SetValue("SELECT 'ok' AS r;")
	m.onDBMsg(m.dbRunAtCursor(v)())
	if v.res == nil || v.res.Rows[0][0].Text != "ok" {
		t.Fatalf("resultado: %+v %s", v.res, v.msg)
	}
	m.closeDBTab(v)
}

// TestE2ESenhaAoConectar: banco direto sem senha salva, como mysql -h ip -u usuario -p.
func TestE2ESenhaAoConectar(t *testing.T) {
	if os.Getenv("MTSH_DB_TEST") == "" {
		t.Skip("MTSH_DB_TEST não definido")
	}
	m := dbTestModel(t)
	d := config.Database{Name: "pg", Engine: dbconn.Postgres, Host: "127.0.0.1", Port: 15432, User: "postgres", DB: "loja"}
	type_ := func(pass string, remember bool) *dbView {
		m.modal = nil
		for _, v := range append([]*dbView{}, m.dbTabs...) {
			m.closeDBTab(v)
		}
		m.openDB(d)
		pm := m.modal.(*dbPassModal)
		pm.in.SetValue(pass)
		pm.remember = remember
		cmd, _ := pm.update(m, tea.KeyMsg{Type: tea.KeyEnter})
		m.onDBMsg(cmd())
		return m.currentDB()
	}
	// senha errada com "lembrar": não conecta e não grava
	if v := type_("errada", true); v.conn != nil || m.vault.Has(config.DBVaultKey("pg")) {
		t.Fatalf("senha errada: conn=%v salvou=%v", v.conn != nil, m.vault.Has(config.DBVaultKey("pg")))
	}
	// senha certa sem "lembrar": conecta e não grava
	if v := type_("s3nha", false); v.conn == nil || m.vault.Has(config.DBVaultKey("pg")) {
		t.Fatalf("sem lembrar: %s", v.msg)
	}
	// senha certa com "lembrar": grava, e na próxima vez não pergunta
	if v := type_("s3nha", true); v.conn == nil || !m.vault.Has(config.DBVaultKey("pg")) {
		t.Fatalf("com lembrar: %s", v.msg)
	}
	m.closeDBTab(m.currentDB())
	m.modal = nil
	cmd := m.openDB(d)
	if m.modal != nil || cmd == nil {
		t.Fatal("com senha salva não devia perguntar")
	}
	m.onDBMsg(cmd())
	if m.currentDB().conn == nil {
		t.Fatal(m.currentDB().msg)
	}
}
