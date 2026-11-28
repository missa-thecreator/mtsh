package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"

	"mtsh/internal/config"
	"mtsh/internal/dbconn"
)

// Export do resultado pra CSV: roda a consulta de novo gravando tudo no
// arquivo (sem o limite de linhas da grade).

var limitRe = regexp.MustCompile(`(?i)\blimit\s+\d+`)

type dbExportedMsg struct {
	v    *dbView
	path string
	n    int
	err  error
}

// dbExportTickMsg redesenha o andamento ("exportando… 12345 linhas").
type dbExportTickMsg struct{ v *dbView }

func exportTick(v *dbView) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return dbExportTickMsg{v} })
}

type dbExportModal struct {
	v     *dbView
	path  textinput.Model
	focus int // 0 arquivo, 1 botão Exportar
	err   string
}

// exportDir é onde o arquivo vai por padrão: Downloads, se existir; senão a pasta pessoal.
func exportDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if st, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && st.IsDir() {
		return filepath.Join(home, "Downloads")
	}
	return home
}

// tildePath troca a pasta pessoal por ~ (mais curto de ler no campo).
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func newDBExportModal(v *dbView) *dbExportModal {
	name := strings.ReplaceAll(dbconn.TableOf(v.resStmt), ".", "_")
	if name == "" {
		name = "consulta"
	}
	e := &dbExportModal{v: v, path: newInput("", 50)}
	e.path.CharLimit = 1024
	e.path.SetValue(tildePath(filepath.Join(exportDir(), name+"-"+time.Now().Format("20060102-150405")+".csv")))
	e.setFocus(0)
	return e
}

func (e *dbExportModal) setFocus(f int) {
	e.focus = f
	if f == 0 {
		e.path.Focus()
		e.path.CursorEnd()
	} else {
		e.path.Blur()
	}
}

// submit confere o arquivo e começa o export; devolve se a janela fecha.
func (e *dbExportModal) submit() (tea.Cmd, bool) {
	p := strings.TrimSpace(e.path.Value())
	if p == "" {
		e.err = "diga onde salvar o arquivo"
		return nil, false
	}
	p = config.ExpandHome(p)
	if _, err := os.Stat(p); err == nil {
		e.err = "esse arquivo já existe; mude o nome"
		return nil, false
	}
	if st, err := os.Stat(filepath.Dir(p)); err != nil || !st.IsDir() {
		e.err = "a pasta não existe: " + tildePath(filepath.Dir(p))
		return nil, false
	}
	cmd := e.v.export(p)
	return cmd, cmd != nil
}

func (e *dbExportModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:ok", mm):
			return e.submit()
		case clicked("m:cancel", mm), clicked("m:close", mm):
			return nil, true
		case clicked("x:path", mm):
			e.setFocus(0)
		}
		return nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	e.err = ""
	switch k.String() {
	case "esc":
		return nil, true
	case "enter":
		return e.submit()
	case "tab", "shift+tab", "down", "up":
		e.setFocus(1 - e.focus)
		return nil, false
	}
	if e.focus != 0 {
		return nil, false
	}
	var cmd tea.Cmd
	e.path, cmd = e.path.Update(msg)
	return cmd, false
}

func (e *dbExportModal) view(m *Model) string {
	rows := []string{
		sDim.Render("  " + runewidth.Truncate(oneLine(e.v.resStmt), 62, "…")),
		"",
		fieldRow("Arquivo", e.focus == 0, zone.Mark("x:path", fieldBox(e.path.View(), 50))),
		"",
	}
	switch {
	case e.err != "":
		rows = append(rows, sErr.Render("  "+e.err))
	case limitRe.MatchString(e.v.resStmt):
		// abrir uma tabela pela lista gera LIMIT 100: o export levaria só isso
		rows = append(rows, sAcc.Render("  a consulta tem LIMIT: só essas linhas vão pro arquivo"),
			sFaint.Render("  pra levar tudo, tire o LIMIT e rode de novo (F5)"))
	default:
		rows = append(rows, sFaint.Render("  roda a consulta de novo e grava todas as linhas, sem limite"))
	}
	btns := button("m:cancel", " Cancelar ", false, false) + "  " + button("m:ok", " Exportar ", true, e.focus == 1)
	rows = append(rows, "", lipgloss.PlaceHorizontal(64, lipgloss.Right, btns), "")
	return dialog("Exportar para CSV", 72, strings.Join(rows, "\n"))
}

// export roda a consulta do resultado de novo gravando em path; ctrl+c cancela.
func (v *dbView) export(path string) tea.Cmd {
	if v.conn == nil {
		return nil
	}
	if v.running != nil {
		v.say("espere o comando terminar (ctrl+c cancela)", true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	count := &atomic.Int64{}
	v.running, v.runText, v.exporting = cancel, "exportando para "+tildePath(path), count
	v.say("", false)
	db, stmt := v.conn.db, v.resStmt
	run := func() tea.Msg {
		defer cancel()
		n, err := exportFile(ctx, db, stmt, path, count)
		if errors.Is(ctx.Err(), context.Canceled) && err != nil {
			err = errors.New("cancelado")
		}
		return dbExportedMsg{v, path, n, err}
	}
	return tea.Batch(run, exportTick(v))
}

// exportFile grava num arquivo provisório e só dá o nome certo no fim: um
// export cancelado ou com erro não deixa arquivo pela metade.
func exportFile(ctx context.Context, db *sql.DB, stmt, path string, count *atomic.Int64) (int, error) {
	tmp := path + ".parcial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := dbconn.ExportCSV(ctx, db, stmt, f, count)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return n, err
}

func (m *Model) onDBExportMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dbExportTickMsg:
		if msg.v.exporting != nil && !msg.v.closed {
			return exportTick(msg.v)
		}
	case dbExportedMsg:
		v := msg.v
		v.running, v.exporting = nil, nil
		if v.closed {
			return nil
		}
		if msg.err != nil {
			v.say("export não terminou: "+msg.err.Error(), true)
			return nil
		}
		v.say(fmt.Sprintf("✓ %s exportadas em %s", plural(msg.n, "linha", "linhas"), tildePath(msg.path)), false)
	}
	return nil
}
