package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pkg/sftp"

	"mtsh/internal/config"
)

// pipeSftp liga um cliente a um servidor SFTP local (que mexe no disco de verdade).
func pipeSftp(t *testing.T) *sftp.Client {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	c, err := sftp.NewClientPipe(cr, cw, sftp.UseConcurrentWrites(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close(); c.Close() }) // servidor primeiro: o cliente espera o fim da leitura
	return c
}

func TestSftpCopyTree(t *testing.T) {
	c := pipeSftp(t)
	local, remote := t.TempDir(), t.TempDir()
	big := strings.Repeat("x", 3<<20) // vários pacotes: passa pelo envio em paralelo
	os.MkdirAll(filepath.Join(local, "proj", "sub"), 0o755)
	os.WriteFile(filepath.Join(local, "proj", "a.sh"), []byte("echo oi\n"), 0o750)
	os.WriteFile(filepath.Join(local, "proj", "sub", "big"), []byte(big), 0o644)

	// envia a pasta inteira
	j := &job{}
	if err := copyTree(localFS{}, filepath.Join(local, "proj"), remoteFS{c}, filepath.Join(remote, "proj"), j, false); err != nil {
		t.Fatal(err)
	}
	if j.done.Load() != j.total.Load() || j.total.Load() != int64(len(big)+8) {
		t.Fatalf("progresso %d/%d", j.done.Load(), j.total.Load())
	}
	got, _ := os.ReadFile(filepath.Join(remote, "proj", "sub", "big"))
	if string(got) != big {
		t.Fatal("arquivo grande chegou diferente")
	}
	if st, _ := os.Stat(filepath.Join(remote, "proj", "a.sh")); st.Mode().Perm() != 0o750 {
		t.Fatalf("permissão = %v", st.Mode().Perm())
	}

	// baixa de volta pra outro lugar
	back := filepath.Join(local, "volta")
	if err := copyTree(remoteFS{c}, filepath.Join(remote, "proj"), localFS{}, back, &job{}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(back, "sub", "big")); string(got) != big {
		t.Fatal("download chegou diferente")
	}

	// cancelado antes de começar não copia nada
	j = &job{}
	j.cancel.Store(true)
	if err := copyTree(localFS{}, filepath.Join(local, "proj"), remoteFS{c}, filepath.Join(remote, "outra"), j, false); err != errCanceled {
		t.Fatalf("cancelar: %v", err)
	}

	// painel remoto: pastas primeiro, ".." no topo
	p := &pane{fs: remoteFS{c}}
	p.load(filepath.Join(remote, "proj"))
	if len(p.items) != 3 || p.items[0].name != ".." || p.items[1].name != "sub" || !p.items[1].dir {
		t.Fatalf("itens = %+v", p.items)
	}
	p.sel = 1
	p.load(filepath.Join(remote, "proj", "sub"))
	p.up()
	if p.items[p.sel].name != "sub" {
		t.Fatal("subir devia destacar a pasta de onde saiu")
	}
}

func TestSftpParallelJobs(t *testing.T) {
	v := &sftpView{panes: [2]*pane{{fs: localFS{}}, {fs: localFS{}}}}
	noop := func(_, _ fsys, _ *job, _ bool) error { return nil }
	if v.startJob("a", "/r/a", 0, noop) == nil || v.startJob("b", "/r/b", 0, noop) == nil {
		t.Fatal("caminhos diferentes deviam rodar juntos")
	}
	if v.startJob("a de novo", "/r/a", 0, noop) != nil || v.startJob("dentro", "/r/b/x", 0, noop) != nil || v.startJob("pai", "/r", 0, noop) != nil {
		t.Fatal("mesmo caminho (ou dentro dele) devia esperar")
	}
	if len(v.jobs) != 2 {
		t.Fatalf("jobs = %d", len(v.jobs))
	}
	m := &Model{}
	m.onSftpMsg(sftpDoneMsg{v, v.jobs[0], nil})
	m.onSftpMsg(sftpDoneMsg{v, v.jobs[0], nil})
	if len(v.jobs) != 0 || v.msg != "✓ 2 concluídas" || v.finished != 0 {
		t.Fatalf("jobs=%d msg=%q finished=%d", len(v.jobs), v.msg, v.finished)
	}
}

func TestSftpCancelOne(t *testing.T) {
	d := t.TempDir()
	v := &sftpView{on: true, panes: [2]*pane{{fs: localFS{}, dir: d}, {fs: localFS{}, dir: d}}}
	noop := func(_, _ fsys, _ *job, _ bool) error { return nil }
	v.startJob("a", "/a", 0, noop)
	v.startJob("b", "/b", 0, noop)
	m := &Model{}
	key := func(k string) {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if k == "tab" {
			msg = tea.KeyMsg{Type: tea.KeyTab}
		} else if k == "down" {
			msg = tea.KeyMsg{Type: tea.KeyDown}
		}
		m.sftpKey(v, msg)
	}
	key("tab") // 0 -> 1
	key("tab") // 1 -> lista
	key("down")
	key("x")
	if v.act != 2 || v.jobs[0].cancel.Load() || !v.jobs[1].cancel.Load() {
		t.Fatalf("act=%d cancel=[%v %v]", v.act, v.jobs[0].cancel.Load(), v.jobs[1].cancel.Load())
	}
	m.onSftpMsg(sftpDoneMsg{v, v.jobs[1], errCanceled})
	m.onSftpMsg(sftpDoneMsg{v, v.jobs[0], nil})
	// a cancelada fica na lista, parada, esperando ↻ ou ×
	if len(v.jobs) != 1 || v.jobs[0].failed != "cancelado" || v.running(nil) != 0 {
		t.Fatalf("jobs=%d failed=%q", len(v.jobs), v.jobs[0].failed)
	}
	key("x") // remove a parada
	if v.act != 1 || len(v.jobs) != 0 {
		t.Fatalf("lista vazia devia voltar pro painel: act=%d jobs=%d", v.act, len(v.jobs))
	}
}

func TestSftpPause(t *testing.T) {
	c := pipeSftp(t)
	local, remote := t.TempDir(), t.TempDir()
	data := strings.Repeat("y", 1<<20)
	os.WriteFile(filepath.Join(local, "f"), []byte(data), 0o644)
	j := &job{}
	j.paused.Store(true)
	done := make(chan error)
	go func() {
		done <- copyTree(localFS{}, filepath.Join(local, "f"), remoteFS{c}, filepath.Join(remote, "f"), j, false)
	}()
	time.Sleep(300 * time.Millisecond)
	if j.done.Load() != 0 {
		t.Fatalf("pausado mas copiou %d bytes", j.done.Load())
	}
	j.paused.Store(false) // continua de onde parou
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(remote, "f")); string(got) != data {
		t.Fatal("arquivo diferente depois de continuar")
	}
	// cancelar enquanto pausado também solta a cópia
	j = &job{}
	j.paused.Store(true)
	go func() {
		done <- copyTree(localFS{}, filepath.Join(local, "f"), remoteFS{c}, filepath.Join(remote, "g"), j, false)
	}()
	time.Sleep(150 * time.Millisecond)
	j.cancel.Store(true)
	if err := <-done; err != errCanceled {
		t.Fatalf("cancelar pausado: %v", err)
	}
}

func TestSftpResume(t *testing.T) {
	c := pipeSftp(t)
	local, remote := t.TempDir(), t.TempDir()
	data := strings.Repeat("0123456789", 300_000) // 3 MB
	os.MkdirAll(filepath.Join(local, "d"), 0o755)
	os.WriteFile(filepath.Join(local, "d", "inteiro"), []byte("pronto"), 0o644)
	os.WriteFile(filepath.Join(local, "d", "grande"), []byte(data), 0o644)
	// como se a cópia anterior tivesse caído: um inteiro, o outro pela metade
	os.MkdirAll(filepath.Join(remote, "d"), 0o755)
	os.WriteFile(filepath.Join(remote, "d", "inteiro"), []byte("pronto"), 0o644)
	os.WriteFile(filepath.Join(remote, "d", "grande"), []byte(data[:1_234_567]), 0o644)

	for _, dir := range []struct {
		name     string
		src, dst fsys
		sp, dp   string
	}{
		{"envio", localFS{}, remoteFS{c}, filepath.Join(local, "d"), filepath.Join(remote, "d")},
		{"download", remoteFS{c}, localFS{}, filepath.Join(remote, "d"), filepath.Join(local, "volta")},
	} {
		if dir.name == "download" { // metade baixada
			os.MkdirAll(dir.dp, 0o755)
			os.WriteFile(filepath.Join(dir.dp, "grande"), []byte(data[:777_777]), 0o644)
		}
		j := &job{}
		if err := copyTree(dir.src, dir.sp, dir.dst, dir.dp, j, true); err != nil {
			t.Fatalf("%s: %v", dir.name, err)
		}
		got, _ := os.ReadFile(filepath.Join(dir.dp, "grande"))
		if string(got) != data {
			t.Fatalf("%s: arquivo retomado diferente (%d de %d bytes)", dir.name, len(got), len(data))
		}
		if j.done.Load() != j.total.Load() {
			t.Fatalf("%s: progresso %d/%d", dir.name, j.done.Load(), j.total.Load())
		}
	}

	// sem resume (substituir): destino maior é sobrescrito, não continuado
	os.WriteFile(filepath.Join(remote, "d", "inteiro"), []byte("bem maior que o original"), 0o644)
	if err := copyTree(localFS{}, filepath.Join(local, "d", "inteiro"), remoteFS{c}, filepath.Join(remote, "d", "inteiro"), &job{}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(remote, "d", "inteiro")); string(got) != "pronto" {
		t.Fatalf("substituir: %q", got)
	}
}

func TestSplitDropped(t *testing.T) {
	got := splitDropped(`"/mnt/c/Users/Ma theus/a b.txt" /mnt/c/x.sql 'C:\Users\M\c d.zip'` + "\r\n/tmp/y")
	want := []string{"/mnt/c/Users/Ma theus/a b.txt", "/mnt/c/x.sql", `C:\Users\M\c d.zip`, "/tmp/y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestSftpDrop(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(local, "um arquivo.txt"), []byte("oi"), 0o644)
	os.WriteFile(filepath.Join(remote, "um arquivo.txt"), []byte("velho"), 0o644)
	os.WriteFile(filepath.Join(local, "novo"), nil, 0o644)
	v := &sftpView{on: true, panes: [2]*pane{{fs: localFS{}, dir: local}, {fs: localFS{}, dir: remote}}}
	m := &Model{}
	drop := tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(`"` + filepath.Join(local, "um arquivo.txt") + `" ` + filepath.Join(local, "novo"))}
	m.sftpKey(v, drop)
	if m.modal == nil || len(v.jobs) != 0 {
		t.Fatal("um já existe: devia perguntar antes de enviar")
	}
	m.modal.(*confirmModal).onOK(m)
	if len(v.jobs) != 2 || v.jobs[0].target != filepath.Join(remote, "um arquivo.txt") || v.jobs[1].target != filepath.Join(remote, "novo") {
		t.Fatalf("jobs = %d", len(v.jobs))
	}
	// caminho que não existe não envia nada
	v2 := &sftpView{on: true, panes: v.panes}
	m.sftpKey(v2, tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("/nao/existe")})
	if len(v2.jobs) != 0 || !v2.msgErr {
		t.Fatal("caminho inexistente devia avisar")
	}
}

func TestSftpEscVolta(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "a", "b"), 0o755)
	os.MkdirAll(filepath.Join(d, "z"), 0o755)
	p := &pane{fs: localFS{}}
	p.load(d)
	v := &sftpView{on: true, panes: [2]*pane{p, {fs: localFS{}, dir: d}}}
	m := &Model{cfg: &config.File{}, width: 120, height: 40}
	key := func(t tea.KeyType) { m.sftpKey(v, tea.KeyMsg{Type: t}) }
	enterOn := func(name string) {
		for i, it := range p.items {
			if it.name == name {
				p.sel = i
			}
		}
		key(tea.KeyEnter)
	}
	enterOn("a")
	enterOn("b")
	if p.dir != filepath.Join(d, "a", "b") {
		t.Fatalf("dir = %s", p.dir)
	}
	key(tea.KeyEsc)
	if p.dir != filepath.Join(d, "a") || p.items[p.sel].name != "b" || !v.on {
		t.Fatalf("esc 1: dir=%s sel=%s on=%v", p.dir, p.items[p.sel].name, v.on)
	}
	key(tea.KeyEsc)
	if p.dir != d || p.items[p.sel].name != "a" || !v.on {
		t.Fatalf("esc 2: dir=%s on=%v", p.dir, v.on)
	}
	key(tea.KeyEsc) // já na primeira pasta: sai pro terminal
	if v.on || p.dir != d {
		t.Fatalf("esc 3: devia sair do SFTP (on=%v dir=%s)", v.on, p.dir)
	}
	// subir com backspace também entra no caminho: esc desce de volta
	v.on = true
	key(tea.KeyBackspace)
	key(tea.KeyEsc)
	if p.dir != d || !v.on {
		t.Fatalf("depois de subir, esc devia voltar pra %s (dir=%s)", d, p.dir)
	}
}
