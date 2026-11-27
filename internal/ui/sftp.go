package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/pkg/sftp"

	"mtsh/internal/config"
	"mtsh/internal/fonts"
	"mtsh/internal/session"
)

// ---- sistema de arquivos: o mesmo jeito de mexer no local e no remoto ----

type fsys interface {
	ReadDir(string) ([]os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
	Open(string) (io.ReadCloser, error)
	Create(string) (io.WriteCloser, error)
	OpenWrite(string) (io.WriteCloser, error) // sem truncar, pra continuar um arquivo pela metade
	Mkdir(string) error
	Chmod(string, os.FileMode) error
	Rename(string, string) error
	RemoveAll(string) error
}

type localFS struct{}

func (localFS) ReadDir(p string) ([]os.FileInfo, error) {
	des, err := os.ReadDir(p)
	out := make([]os.FileInfo, 0, len(des))
	for _, d := range des {
		if fi, err := d.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out, err
}
func (localFS) Stat(p string) (os.FileInfo, error)      { return os.Stat(p) }
func (localFS) Open(p string) (io.ReadCloser, error)    { return os.Open(p) }
func (localFS) Create(p string) (io.WriteCloser, error) { return os.Create(p) }
func (localFS) OpenWrite(p string) (io.WriteCloser, error) {
	return os.OpenFile(p, os.O_WRONLY, 0)
}
func (localFS) Mkdir(p string) error                { return os.Mkdir(p, 0o755) }
func (localFS) Chmod(p string, m os.FileMode) error { return os.Chmod(p, m) }
func (localFS) Rename(a, b string) error            { return os.Rename(a, b) }
func (localFS) RemoveAll(p string) error            { return os.RemoveAll(p) }

type remoteFS struct{ c *sftp.Client }

func (r remoteFS) ReadDir(p string) ([]os.FileInfo, error) { return r.c.ReadDir(p) }
func (r remoteFS) Stat(p string) (os.FileInfo, error)      { return r.c.Stat(p) }
func (r remoteFS) Open(p string) (io.ReadCloser, error) {
	f, err := r.c.Open(p)
	if err != nil {
		return nil, err // não devolve um *File nulo dentro da interface
	}
	return f, nil
}
func (r remoteFS) Create(p string) (io.WriteCloser, error) {
	f, err := r.c.Create(p)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (r remoteFS) OpenWrite(p string) (io.WriteCloser, error) {
	f, err := r.c.OpenFile(p, os.O_WRONLY)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (r remoteFS) Mkdir(p string) error                { return r.c.Mkdir(p) }
func (r remoteFS) Chmod(p string, m os.FileMode) error { return r.c.Chmod(p, m) }
func (r remoteFS) Rename(a, b string) error            { return r.c.Rename(a, b) }
func (r remoteFS) RemoveAll(p string) error            { return r.c.RemoveAll(p) }

// walk visita p e tudo dentro dele (pastas antes do conteúdo). rel é o caminho
// relativo a p ("" pro próprio p). Links pra pasta lá dentro ficam de fora,
// pra não entrar em ciclo.
func walk(fs fsys, p, rel string, fn func(rel string, fi os.FileInfo) error) error {
	fi, err := fs.Stat(p)
	if err != nil {
		return err
	}
	if err := fn(rel, fi); err != nil || !fi.IsDir() {
		return err
	}
	ents, err := fs.ReadDir(p)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Mode()&os.ModeSymlink != 0 {
			if st, err := fs.Stat(path.Join(p, e.Name())); err == nil && st.IsDir() {
				continue
			}
		}
		if err := walk(fs, path.Join(p, e.Name()), path.Join(rel, e.Name()), fn); err != nil {
			return err
		}
	}
	return nil
}

// ---- transferência em segundo plano ----

var errCanceled = errors.New("cancelado")

// job é a operação em andamento (cópia ou exclusão), lida pela tela a cada tick.
type job struct {
	label       string
	target      string // caminho que a operação escreve ou apaga
	total, done atomic.Int64
	cancel      atomic.Bool
	paused      atomic.Bool

	// pra rodar de novo (↻), inclusive depois de reconectar: o sistema de
	// arquivos de cada lado é pego na hora, do painel from e do outro
	from   int
	do     func(src, dst fsys, j *job, resume bool) error
	failed string // "" = rodando; senão o motivo, e ela fica na lista esperando ↻ ou ×

	// pra retomar (↻) sem confiar só no tamanho: arquivos do destino que esta
	// cópia já gravou inteiros, e o que ela estava gravando quando parou.
	// Só a goroutine da cópia mexe neles (uma retomada só começa depois que a anterior acabou).
	finished map[string]bool
	partial  string
}

// wait segura a cópia enquanto está pausada; erro se foi cancelada.
// A conexão continua de pé, então continuar retoma do mesmo byte.
func (j *job) wait() error {
	for j.paused.Load() && !j.cancel.Load() {
		time.Sleep(100 * time.Millisecond)
	}
	if j.cancel.Load() {
		return errCanceled
	}
	return nil
}

type countR struct {
	r    io.Reader
	size int64
	j    *job
}

func (c *countR) Read(b []byte) (int, error) {
	if err := c.j.wait(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(b)
	c.j.done.Add(int64(n))
	return n, err
}

// Size deixa o sftp enviar em paralelo (bem mais rápido com latência).
func (c *countR) Size() int64 { return c.size }

type countW struct {
	w io.Writer
	j *job
}

func (c *countW) Write(b []byte) (int, error) {
	if err := c.j.wait(); err != nil {
		return 0, err
	}
	n, err := c.w.Write(b)
	c.j.done.Add(int64(n))
	return n, err
}

// copyFile copia um arquivo. Com resume (↻), o que esta cópia já gravou
// inteiro é pulado e o arquivo em que ela parou continua do tamanho que tem.
// O resto é copiado do zero: um arquivo que já estava no destino antes (versão
// antiga, ao substituir uma pasta) não é pedaço desta cópia, mesmo sendo menor.
func copyFile(src fsys, sp string, dst fsys, dp string, fi os.FileInfo, j *job, resume bool) error {
	var off int64
	if resume {
		if j.finished[dp] {
			j.done.Add(fi.Size())
			return nil
		}
		if st, err := dst.Stat(dp); err == nil && dp == j.partial && !st.IsDir() && st.Size() <= fi.Size() {
			off = st.Size()
		}
	}
	j.partial = dp
	in, err := src.Open(sp)
	if err != nil {
		return err
	}
	defer in.Close()
	var out io.WriteCloser
	if off > 0 {
		out, err = dst.OpenWrite(dp)
	} else {
		out, err = dst.Create(dp)
	}
	if err != nil {
		return err
	}
	if off > 0 {
		// os.File e sftp.File sabem pular pra posição
		if _, err = in.(io.Seeker).Seek(off, io.SeekStart); err == nil {
			_, err = out.(io.Seeker).Seek(off, io.SeekStart)
		}
		if err != nil {
			out.Close()
			return err
		}
		j.done.Add(off)
	}
	if rf, ok := in.(*sftp.File); ok {
		_, err = rf.WriteTo(&countW{out, j}) // download: leitura em paralelo
	} else {
		_, err = io.Copy(out, &countR{in, fi.Size() - off, j})
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := dst.Chmod(dp, fi.Mode().Perm()); err != nil {
		return err
	}
	if j.finished == nil {
		j.finished = map[string]bool{}
	}
	j.finished[dp] = true
	return nil
}

// copyTree copia o arquivo ou a pasta sp (de src) pra dp (em dst).
func copyTree(src fsys, sp string, dst fsys, dp string, j *job, resume bool) error {
	err := walk(src, sp, "", func(_ string, fi os.FileInfo) error {
		if !fi.IsDir() {
			j.total.Add(fi.Size())
		}
		return nil
	})
	if err != nil {
		return err
	}
	return walk(src, sp, "", func(rel string, fi os.FileInfo) error {
		if j.cancel.Load() {
			return errCanceled
		}
		to := path.Join(dp, rel)
		if fi.IsDir() {
			if st, err := dst.Stat(to); err == nil && st.IsDir() {
				return nil
			}
			return dst.Mkdir(to)
		}
		return copyFile(src, path.Join(sp, rel), dst, to, fi, j, resume)
	})
}

// ---- painéis ----

type sfItem struct {
	name string
	dir  bool
	size int64
}

type pane struct {
	fs       fsys
	dir      string
	items    []sfItem
	sel, off int
	err      string
	back     []string // pastas por onde passou: o esc volta por elas
}

// load lê a pasta; se é a mesma de antes, mantém o item destacado.
func (p *pane) load(dir string) {
	keep := ""
	if dir == p.dir && p.sel < len(p.items) {
		keep = p.items[p.sel].name
	}
	ents, err := p.fs.ReadDir(dir) // shortcut: lê na hora, trava a tela em pasta remota enorme ou link lento; passar pra tea.Cmd se incomodar
	if err != nil {
		p.err = err.Error()
		if p.dir != "" {
			return // fica onde estava
		}
	} else {
		p.err = ""
	}
	p.dir, p.items, p.sel, p.off = dir, nil, 0, 0
	if dir != "/" {
		p.items = append(p.items, sfItem{"..", true, 0})
	}
	var list []sfItem
	for _, e := range ents {
		isDir := e.IsDir()
		if e.Mode()&os.ModeSymlink != 0 {
			if st, err := p.fs.Stat(path.Join(dir, e.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		list = append(list, sfItem{e.Name(), isDir, e.Size()})
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].dir != list[b].dir {
			return list[a].dir
		}
		return strings.ToLower(list[a].name) < strings.ToLower(list[b].name)
	})
	p.items = append(p.items, list...)
	for i, it := range p.items {
		if it.name == keep {
			p.sel = i
		}
	}
}

func (p *pane) move(d, rows int) {
	p.sel = max(0, min(len(p.items)-1, p.sel+d))
	if p.sel < p.off {
		p.off = p.sel
	}
	if p.sel >= p.off+rows {
		p.off = p.sel - rows + 1
	}
}

func (p *pane) cur() (sfItem, bool) {
	if p.sel < len(p.items) && p.items[p.sel].name != ".." {
		return p.items[p.sel], true
	}
	return sfItem{}, false
}

// goTo navega pra dir lembrando de onde saiu (se a pasta abriu).
func (p *pane) goTo(dir string) {
	prev := p.dir
	p.load(dir)
	if p.dir == dir && prev != dir && prev != "" {
		p.back = append(p.back, prev)
	}
}

func (p *pane) up() {
	from := p.dir
	p.goTo(path.Dir(p.dir))
	p.highlightChild(from)
}

// goBack volta pra pasta anterior; false se já está na primeira.
func (p *pane) goBack() bool {
	if len(p.back) == 0 {
		return false
	}
	from := p.dir
	p.load(p.back[len(p.back)-1])
	p.back = p.back[:len(p.back)-1]
	p.highlightChild(from)
	return true
}

// highlightChild destaca a pasta de onde saiu, se ela está na pasta atual.
func (p *pane) highlightChild(from string) {
	if path.Dir(from) != p.dir {
		return
	}
	for i, it := range p.items {
		if it.name == path.Base(from) {
			p.sel = i
		}
	}
}

// ---- a tela de SFTP de uma aba ----

// sftpView é o gerenciador de arquivos de uma aba de sessão. A conexão SFTP
// passa pelo socket de controle da própria aba (ctl), então não loga de novo.
type sftpView struct {
	ctl, target string
	on          bool // mostrando o SFTP no lugar do terminal
	connecting  bool
	closed      bool
	client      *sftp.Client
	cmd         *exec.Cmd
	panes       [2]*pane // 0 local, 1 remoto
	act         int      // 0 local, 1 remoto, 2 lista de transferências
	jobSel      int      // transferência destacada (com act == 2)
	jobs        []*job   // em andamento (em paralelo) e as paradas, esperando ↻ ou ×
	remoteDir   string   // pasta remota pra abrir ao conectar (a de antes de reconectar)
	finished    int      // concluídas desde que a fila esvaziou pela última vez
	jobFailed   bool     // alguma dessas falhou: o erro fica à vista até a fila esvaziar
	msg         string
	msgErr      bool
}

func newSftpView(ctl, target string) *sftpView {
	v := &sftpView{ctl: ctl, target: target}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	v.panes[0] = &pane{fs: localFS{}}
	v.panes[0].load(home)
	return v
}

// adopt traz da tela antiga (antes de reconectar) as transferências paradas,
// as pastas e se o SFTP estava à vista.
func (v *sftpView) adopt(old *sftpView) {
	v.panes[0], v.on = old.panes[0], old.on
	if old.panes[1] != nil {
		v.remoteDir = old.panes[1].dir
	}
	for _, j := range old.jobs {
		if j.failed != "" {
			v.jobs = append(v.jobs, j)
		}
	}
}

func (v *sftpView) say(text string, isErr bool) { v.msg, v.msgErr = text, isErr }

func (v *sftpView) close() {
	v.closed = true
	v.cancelJobs()
	if v.client != nil {
		v.client.Close()
		go v.cmd.Wait()
	}
}

type sftpReadyMsg struct {
	v      *sftpView
	client *sftp.Client
	cmd    *exec.Cmd
	home   string
	err    error
}

type sftpDoneMsg struct {
	v   *sftpView
	j   *job
	err error
}

type sftpTickMsg struct{ v *sftpView }

func sftpTick(v *sftpView) tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return sftpTickMsg{v} })
}

func sftpConnect(v *sftpView, stop func() bool) tea.Cmd {
	ctl, target := v.ctl, v.target
	return func() tea.Msg {
		if !waitCtl(ctl, stop) {
			return sftpReadyMsg{v: v, err: errors.New("a sessão ainda não conectou")}
		}
		cmd := exec.Command("ssh", "-S", ctl, "-o", "ControlMaster=no", "-o", "BatchMode=yes", "-T", "-s", "--", target, "sftp")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		w, err := cmd.StdinPipe()
		if err != nil {
			return sftpReadyMsg{v: v, err: err}
		}
		r, err := cmd.StdoutPipe()
		if err != nil {
			return sftpReadyMsg{v: v, err: err}
		}
		if err := cmd.Start(); err != nil {
			return sftpReadyMsg{v: v, err: err}
		}
		c, err := sftp.NewClientPipe(r, w, sftp.UseConcurrentWrites(true))
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			if s := strings.TrimSpace(stderr.String()); s != "" {
				err = fmt.Errorf("%w: %s", err, s)
			}
			return sftpReadyMsg{v: v, err: err}
		}
		home, err := c.Getwd()
		if err != nil {
			home = "/"
		}
		return sftpReadyMsg{v: v, client: c, cmd: cmd, home: home}
	}
}

// sftpOf devolve a tela de SFTP da aba s (criada no connect).
func (m *Model) sftpOf(s *session.Session) *sftpView { return m.sftp[s] }

func (m *Model) toggleSftp(s *session.Session) tea.Cmd {
	v := m.sftpOf(s)
	if v == nil {
		return nil
	}
	v.on = !v.on
	if !v.on || v.client != nil || v.connecting {
		return nil
	}
	if done, _ := s.Exited(); done {
		v.say("sessão desconectada: reconecte pra usar o SFTP", true)
		return nil
	}
	v.connecting = true
	v.say("conectando o SFTP…", false)
	return sftpConnect(v, func() bool { done, _ := s.Exited(); return done || v.closed })
}

func (m *Model) onSftpMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sftpReadyMsg:
		v := msg.v
		v.connecting = false
		if v.closed {
			if msg.client != nil {
				msg.client.Close()
				go msg.cmd.Wait()
			}
			return nil
		}
		if msg.err != nil {
			v.say("SFTP não conectou: "+msg.err.Error(), true)
			return nil
		}
		v.client, v.cmd = msg.client, msg.cmd
		v.panes[1] = &pane{fs: remoteFS{msg.client}}
		if v.remoteDir != "" {
			v.panes[1].load(v.remoteDir)
		}
		if v.panes[1].dir == "" || v.panes[1].err != "" {
			v.panes[1].load(msg.home)
		}
		v.act = 1
		v.say("", false)
	case sftpTickMsg:
		if msg.v.running(nil) > 0 && !msg.v.closed {
			return sftpTick(msg.v)
		}
	case sftpDoneMsg:
		v := msg.v
		if msg.err == nil {
			v.jobs = slices.DeleteFunc(v.jobs, func(j *job) bool { return j == msg.j })
		} else if msg.j.failed = msg.err.Error(); errors.Is(msg.err, errCanceled) {
			msg.j.failed = "cancelado"
		}
		if v.closed {
			return nil
		}
		for _, p := range v.panes {
			if p != nil {
				p.load(p.dir)
			}
		}
		v.finished++
		switch {
		case errors.Is(msg.err, errCanceled):
			v.say(msg.j.label+": cancelado", true)
			v.jobFailed = true
		case msg.err != nil:
			v.say(msg.j.label+": "+msg.err.Error(), true)
			v.jobFailed = true
		case v.jobFailed:
		case v.finished > 1:
			v.say(fmt.Sprintf("✓ %d concluídas", v.finished), false)
		default:
			v.say("✓ "+msg.j.label, false)
		}
		v.fixJobSel()
		if v.running(nil) == 0 {
			v.finished, v.jobFailed = 0, false
		}
	}
	return nil
}

// startJob roda do em segundo plano, junto com as outras. from é o painel
// de origem (o outro é o destino).
func (v *sftpView) startJob(label, target string, from int, do func(src, dst fsys, j *job, resume bool) error) tea.Cmd {
	if o := v.conflict(target, nil); o != nil {
		v.say("espere terminar: "+o.label, true)
		return nil
	}
	// uma parada pro mesmo caminho perde o sentido com a nova
	v.jobs = slices.DeleteFunc(v.jobs, func(o *job) bool { return o.failed != "" && o.target == target })
	j := &job{label: label, target: target, from: from, do: do}
	v.jobs = append(v.jobs, j)
	return v.launch(j, false)
}

// conflict acha outra em andamento no mesmo caminho (ou dentro dele):
// duas cópias pro mesmo arquivo o corromperiam.
func (v *sftpView) conflict(target string, except *job) *job {
	for _, o := range v.jobs {
		if o == except || o.failed != "" {
			continue
		}
		if o.target == target || strings.HasPrefix(target, o.target+"/") || strings.HasPrefix(o.target, target+"/") {
			return o
		}
	}
	return nil
}

// running conta as em andamento (fora except).
func (v *sftpView) running(except *job) int {
	n := 0
	for _, o := range v.jobs {
		if o != except && o.failed == "" {
			n++
		}
	}
	return n
}

// launch (re)começa j. O sistema de arquivos é pego agora, do painel atual.
func (v *sftpView) launch(j *job, resume bool) tea.Cmd {
	first := v.running(j) == 0
	src, dst := v.panes[j.from].fs, v.panes[1-j.from].fs
	j.failed = ""
	j.cancel.Store(false)
	j.paused.Store(false)
	j.done.Store(0)
	j.total.Store(0)
	run := func() tea.Msg { return sftpDoneMsg{v, j, j.do(src, dst, j, resume)} }
	if !first {
		return run // o tick já está rodando
	}
	v.say("", false)
	return tea.Batch(run, sftpTick(v))
}

// retryJob retoma a parada i: arquivos que já chegaram inteiros são pulados
// e o que ficou pela metade continua de onde parou.
func (v *sftpView) retryJob(i int) tea.Cmd {
	if i < 0 || i >= len(v.jobs) || v.jobs[i].failed == "" {
		return nil
	}
	j := v.jobs[i]
	if !v.ready() {
		v.say("o SFTP ainda não conectou", true)
		return nil
	}
	if o := v.conflict(j.target, j); o != nil {
		v.say("espere terminar: "+o.label, true)
		return nil
	}
	return v.launch(j, true)
}

// removeJob tira da lista uma parada.
func (v *sftpView) removeJob(i int) {
	if i >= 0 && i < len(v.jobs) && v.jobs[i].failed != "" {
		v.jobs = slices.Delete(v.jobs, i, i+1)
		v.fixJobSel()
	}
}

func (v *sftpView) fixJobSel() {
	v.jobSel = max(0, min(v.jobSel, len(v.jobs)-1))
	if len(v.jobs) == 0 && v.act == 2 {
		v.act = 1
	}
}

func (v *sftpView) cancelJobs() {
	for _, j := range v.jobs {
		j.cancel.Store(true)
	}
}

// ready diz se os dois painéis estão prontos (o remoto depende da conexão).
func (v *sftpView) ready() bool { return v.panes[1] != nil }

// transfer copia o item destacado pro outro painel.
func (m *Model) sftpTransfer(v *sftpView) tea.Cmd {
	src, dst := v.panes[v.act], v.panes[1-v.act]
	it, ok := src.cur()
	if !ok {
		return nil
	}
	sp, dp := path.Join(src.dir, it.name), path.Join(dst.dir, it.name)
	arrow := "↑ enviar "
	if v.act == 1 {
		arrow = "↓ baixar "
	}
	from := v.act
	run := func() tea.Cmd {
		return v.startJob(arrow+it.name, dp, from, func(sf, df fsys, j *job, resume bool) error {
			return copyTree(sf, sp, df, dp, j, resume)
		})
	}
	if _, err := dst.fs.Stat(dp); err == nil {
		m.modal = &confirmModal{
			title: "Substituir", okLabel: "Substituir",
			text: it.name + " já existe em\n" + dst.dir + "\nSubstituir?",
			onOK: func(*Model) tea.Cmd { return run() },
		}
		return nil
	}
	return run()
}

func (m *Model) sftpDelete(v *sftpView) {
	p := v.panes[v.act]
	it, ok := p.cur()
	if !ok {
		return
	}
	full, from := path.Join(p.dir, it.name), v.act
	what := "o arquivo"
	if it.dir {
		what = "a pasta (e tudo dentro dela)"
	}
	m.modal = &confirmModal{
		title: "Apagar", okLabel: "Apagar",
		text: "Apagar " + what + "\n" + full + "?",
		onOK: func(*Model) tea.Cmd {
			return v.startJob("apagar "+it.name, full, from, func(sf, _ fsys, _ *job, _ bool) error { return sf.RemoveAll(full) })
		},
	}
}

func (m *Model) sftpRename(v *sftpView) {
	p := v.panes[v.act]
	it, ok := p.cur()
	if !ok {
		return
	}
	m.modal = newPromptModal("Renomear", "Novo nome", it.name, func(name string) string {
		if err := p.fs.Rename(path.Join(p.dir, it.name), path.Join(p.dir, name)); err != nil {
			return err.Error()
		}
		p.load(p.dir)
		p.selectName(name, m.sftpRows(v))
		v.say("✓ renomeado pra "+name, false)
		return ""
	})
}

func (m *Model) sftpMkdir(v *sftpView) {
	p := v.panes[v.act]
	m.modal = newPromptModal("Nova pasta", "Nome", "", func(name string) string {
		if err := p.fs.Mkdir(path.Join(p.dir, name)); err != nil {
			return err.Error()
		}
		p.load(p.dir)
		p.selectName(name, m.sftpRows(v))
		v.say("✓ pasta "+name+" criada", false)
		return ""
	})
}

func (p *pane) selectName(name string, rows int) {
	for i, it := range p.items {
		if it.name == name {
			p.sel = 0
			p.off = 0
			p.move(i, rows)
		}
	}
}

// open entra na pasta destacada ou transfere o arquivo.
func (m *Model) sftpOpen(v *sftpView) tea.Cmd {
	p := v.panes[v.act]
	if p.sel >= len(p.items) {
		return nil
	}
	it := p.items[p.sel]
	switch {
	case it.name == "..":
		p.up()
	case it.dir:
		p.goTo(path.Join(p.dir, it.name))
	default:
		return m.sftpTransfer(v)
	}
	return nil
}

// maxJobLines é quantas transferências aparecem de uma vez embaixo dos painéis.
const maxJobLines = 5

func jobLines(v *sftpView) int { return min(len(v.jobs), maxJobLines) }

// sftpRows é quantas linhas de arquivo cabem em cada painel.
func (m *Model) sftpRows(v *sftpView) int {
	_, rows := m.termSize()
	return max(1, rows-3-jobLines(v)) // título do painel, transferências, status e ajuda
}

// visibleOff é a primeira linha à vista, garantindo o destacado na tela
// (o painel encolhe quando aparecem transferências).
func (p *pane) visibleOff(rows int) int {
	if p.sel >= p.off+rows {
		return p.sel - rows + 1
	}
	return p.off
}

// togglePause pausa ou continua a transferência i.
func (v *sftpView) togglePause(i int) {
	if i >= 0 && i < len(v.jobs) {
		j := v.jobs[i]
		j.paused.Store(!j.paused.Load())
	}
}

// cancelJob cancela a transferência i (ela some da lista quando parar).
func (v *sftpView) cancelJob(i int) {
	if i >= 0 && i < len(v.jobs) {
		v.jobs[i].cancel.Store(true)
	}
}

func (m *Model) sftpKey(v *sftpView, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	if key == "esc" {
		// volta pelas pastas por onde passou; na primeira, sai pro terminal
		if v.act < 2 && v.panes[v.act] != nil && v.panes[v.act].goBack() {
			return nil
		}
		v.on = false
		return nil
	}
	if key == "ctrl+c" && len(v.jobs) > 0 {
		v.cancelJobs()
		return nil
	}
	if !v.ready() {
		return nil
	}
	if k.Paste { // arrastar do Explorer pro terminal cola o caminho do arquivo
		return m.sftpDrop(v, string(k.Runes))
	}
	switch key {
	case "left":
		v.act = 0
		return nil
	case "right":
		v.act = 1
		return nil
	case "tab", "shift+tab":
		n := 2
		if len(v.jobs) > 0 {
			n = 3 // a lista de transferências entra no ciclo
		}
		d := 1
		if key == "shift+tab" {
			d = n - 1
		}
		v.act = (v.act + d) % n
		return nil
	}
	if v.act == 2 {
		switch key {
		case "up", "k":
			v.jobSel = max(0, v.jobSel-1)
		case "down", "j":
			v.jobSel = min(len(v.jobs)-1, v.jobSel+1)
		case "p", " ", "r", "enter":
			if v.jobSel < len(v.jobs) && v.jobs[v.jobSel].failed != "" {
				return v.retryJob(v.jobSel)
			}
			if key == "p" || key == " " {
				v.togglePause(v.jobSel)
			}
		case "x", "delete", "d", "f8":
			if v.jobSel < len(v.jobs) && v.jobs[v.jobSel].failed != "" {
				v.removeJob(v.jobSel)
			} else {
				v.cancelJob(v.jobSel)
			}
		}
		return nil
	}
	p, rows := v.panes[v.act], m.sftpRows(v)
	switch key {
	case "up", "k":
		p.move(-1, rows)
	case "down", "j":
		p.move(1, rows)
	case "pgup":
		p.move(-rows, rows)
	case "pgdown":
		p.move(rows, rows)
	case "home":
		p.move(-len(p.items), rows)
	case "end":
		p.move(len(p.items), rows)
	case "enter":
		return m.sftpOpen(v)
	case "backspace":
		p.up()
	case "f5", "c":
		return m.sftpTransfer(v)
	case "f2", "r":
		m.sftpRename(v)
	case "f7", "n":
		m.sftpMkdir(v)
	case "f8", "delete", "d":
		m.sftpDelete(v)
	case "ctrl+r":
		p.load(p.dir)
	case "w":
		return m.sftpWinHome(v)
	case "o":
		m.sftpExplorer(v)
	}
	return nil
}

// ---- arrastar arquivos pra janela ----

// splitDropped separa os caminhos colados ao soltar arquivos no terminal:
// separados por espaço ou linha, com aspas quando o nome tem espaço.
func splitDropped(text string) []string {
	var out []string
	var cur strings.Builder
	quote, has := rune(0), false
	for _, r := range text {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, has = r, true
		case quote == 0 && (r == ' ' || r == '\n' || r == '\r' || r == '\t'):
			if has {
				out = append(out, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// sftpDrop envia pro painel Remoto os arquivos (ou pastas) cujos caminhos
// chegaram colados. Caminho do Windows (C:\...) vira /mnt/c/... no WSL.
func (m *Model) sftpDrop(v *sftpView, text string) tea.Cmd {
	type item struct{ sp, dp, name string }
	var items []item
	var existing int
	remote := v.panes[1]
	for _, p := range splitDropped(text) {
		sp := config.ExpandHome(winPath(p))
		if _, err := os.Stat(sp); err != nil {
			v.say("não achei "+p+" neste computador", true)
			return nil
		}
		name := path.Base(sp)
		dp := path.Join(remote.dir, name)
		if _, err := remote.fs.Stat(dp); err == nil {
			existing++
		}
		items = append(items, item{sp, dp, name})
	}
	if len(items) == 0 {
		return nil
	}
	run := func() tea.Cmd {
		var cmds []tea.Cmd
		for _, it := range items {
			sp, dp := it.sp, it.dp
			cmds = append(cmds, v.startJob("↑ enviar "+it.name, dp, 0, func(sf, df fsys, j *job, resume bool) error {
				return copyTree(sf, sp, df, dp, j, resume)
			}))
		}
		return tea.Batch(cmds...)
	}
	if existing > 0 {
		what := items[0].name + " já existe"
		if existing > 1 {
			what = fmt.Sprintf("%d itens já existem", existing)
		}
		m.modal = &confirmModal{
			title: "Substituir", okLabel: "Substituir",
			text: what + " em\n" + remote.dir + "\nSubstituir?",
			onOK: func(*Model) tea.Cmd { return run() },
		}
		return nil
	}
	return run()
}

// ---- Windows (no WSL) ----

type winHomeMsg struct {
	v   *sftpView
	dir string
	err error
}

var winHome string // pasta do usuário no Windows, já no formato do WSL (guardada após a 1ª vez)

// sftpWinHome leva o painel Local pra pasta do usuário do Windows (/mnt/c/Users/…).
func (m *Model) sftpWinHome(v *sftpView) tea.Cmd {
	if !fonts.IsWSL() {
		v.say("w só funciona no WSL", true)
		return nil
	}
	v.act = 0
	if winHome != "" {
		v.panes[0].goTo(winHome)
		return nil
	}
	v.say("perguntando ao Windows…", false)
	return func() tea.Msg { // o powershell demora ~1s: fora da tela
		dir, err := fonts.WinFolder("UserProfile")
		return winHomeMsg{v, dir, err}
	}
}

func (m *Model) onWinHome(msg winHomeMsg) {
	if msg.err != nil {
		msg.v.say(msg.err.Error(), true)
		return
	}
	winHome = msg.dir
	msg.v.say("", false)
	msg.v.panes[0].goTo(winHome)
	msg.v.act = 0
}

// sftpExplorer abre a pasta do painel Local no Explorador de Arquivos do Windows.
func (m *Model) sftpExplorer(v *sftpView) {
	if !fonts.IsWSL() {
		v.say("o só funciona no WSL", true)
		return
	}
	dir, err := fonts.WSLPath("-w", v.panes[0].dir)
	if err != nil {
		v.say(err.Error(), true)
		return
	}
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		v.say("não deu pra abrir o Explorer: "+err.Error(), true)
		return
	}
	go cmd.Wait() // o explorer.exe sai com código 1 mesmo quando abre: não dá pra confiar no resultado
	v.say("abrindo "+dir+" no Explorer", false)
}

// sftpMouse trata o mouse na aba quando o SFTP está à vista.
func (m *Model) sftpMouse(v *sftpView, msg tea.MouseMsg) (tea.Cmd, bool) {
	cols, rows := m.termSize()
	x, y := msg.X-(m.sideW()+1), msg.Y-sessionOriginY
	if x < -1 || x >= cols || y < 0 || y >= rows {
		return nil, false
	}
	if msg.Action != tea.MouseActionPress || !v.ready() {
		return nil, true
	}
	n := m.sftpRows(v)
	if msg.Button == tea.MouseButtonLeft {
		for i := range v.jobs {
			failed := v.jobs[i].failed != ""
			if zone.Get(fmt.Sprintf("sf:jp:%d", i)).InBounds(msg) {
				if failed {
					return v.retryJob(i), true
				}
				v.togglePause(i)
				return nil, true
			}
			if zone.Get(fmt.Sprintf("sf:jx:%d", i)).InBounds(msg) {
				if failed {
					v.removeJob(i)
				} else {
					v.cancelJob(i)
				}
				return nil, true
			}
			if zone.Get(fmt.Sprintf("sf:j:%d", i)).InBounds(msg) {
				v.act, v.jobSel = 2, i
				return nil, true
			}
		}
	}
	for i, p := range v.panes {
		if !zone.Get(fmt.Sprintf("sf:p:%d", i)).InBounds(msg) {
			continue
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			p.off = max(0, p.visibleOff(n)-3)
		case tea.MouseButtonWheelDown:
			p.off = max(0, min(len(p.items)-n, p.visibleOff(n)+3))
		case tea.MouseButtonLeft:
			v.act = i
			off := p.visibleOff(n)
			for j := off; j < min(len(p.items), off+n); j++ {
				if zone.Get(fmt.Sprintf("sf:%d:%d", i, j)).InBounds(msg) {
					// primeiro clique destaca, o segundo abre (ou transfere)
					if p.sel == j {
						return m.sftpOpen(v), true
					}
					p.sel = j
				}
			}
		}
	}
	return nil, true
}

// ---- desenho ----

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (m Model) viewSftp(v *sftpView, cols, rows int) []string {
	jl := jobLines(v)
	n := max(1, rows-3-jl)
	lw := (cols - 1) / 2
	widths := [2]int{lw, cols - 1 - lw}
	var cells [2][]string
	for i := range v.panes {
		cells[i] = m.viewPane(v, i, widths[i], n+1)
	}
	sep := sFaint.Render("│")
	out := make([]string, 0, rows)
	for r := 0; r <= n; r++ {
		out = append(out, cells[0][r]+sep+cells[1][r])
	}
	// uma linha por transferência; com mais de maxJobLines, a janela segue a destacada
	off := max(0, min(v.jobSel-jl+1, len(v.jobs)-jl))
	for i := off; i < off+jl; i++ {
		out = append(out, m.viewJob(v, i, cols))
	}
	// linha de status: mensagem ou erro; com transferências, o resumo delas
	status := sDim.Render(" " + v.msg)
	if v.msgErr {
		status = sErr.Render(" " + v.msg)
	}
	if run := v.running(nil); len(v.jobs) > 0 && (v.msg == "" || run > 0 && !v.msgErr) {
		var done, total int64
		for _, j := range v.jobs {
			if j.failed == "" {
				done, total = done+j.done.Load(), total+j.total.Load()
			}
		}
		text := fmt.Sprintf(" %s · %s de %s", plural(run, "transferência", "transferências"), humanSize(done), humanSize(total))
		if stopped := len(v.jobs) - run; stopped > 0 {
			text += fmt.Sprintf(" · %s (↻ retoma)", plural(stopped, "parada", "paradas"))
		}
		status = sDim.Render(text) +
			sFaint.Render("  ·  tab vai pra lista · p pausa/continua · x cancela · ctrl+c cancela tudo")
	}
	out = append(out, lipgloss.NewStyle().MaxWidth(cols).Render(status))
	help := " enter abre/copia · F5 copia · F2 renomeia · F7 pasta · F8 apaga · w Windows · o Explorer · esc volta"
	out = append(out, sFaint.MaxWidth(cols).Render(help))
	return out
}

// viewJob é a linha de uma transferência: nome, barra, quanto foi e o × pra cancelar.
func (m Model) viewJob(v *sftpView, i, cols int) string {
	j := v.jobs[i]
	sel := v.act == 2 && i == v.jobSel
	bg := lipgloss.NewStyle()
	if sel {
		bg = bg.Background(cSel)
	}
	done, total := j.done.Load(), j.total.Load()
	var info string
	switch {
	case j.failed != "":
		why := j.failed
		if total > 0 {
			why += fmt.Sprintf(" em %d%%", int(done*100/total))
		}
		if r := []rune(why); len(r) > 40 {
			why = string(r[:39]) + "…"
		}
		info = sErr.Inherit(bg).Render(why)
	case j.cancel.Load():
		info = sErr.Inherit(bg).Render("cancelando…")
	case j.paused.Load() && total > 0:
		pct := int(done * 100 / total)
		info = sAcc.Inherit(bg).Render(fmt.Sprintf("pausado %3d%%  %s de %s", pct, humanSize(done), humanSize(total)))
	case total == 0:
		info = sDim.Inherit(bg).Render("…")
	default:
		pct := int(done * 100 / total)
		const barW = 16
		fill := min(barW, pct*barW/100)
		info = sAcc.Inherit(bg).Render(strings.Repeat("█", fill)) + sFaint.Inherit(bg).Render(strings.Repeat("░", barW-fill)) +
			sFg.Inherit(bg).Render(fmt.Sprintf(" %3d%%  %s de %s", pct, humanSize(done), humanSize(total)))
	}
	pauseIcon := " ‖ " // ⏸ e ▶ viram emoji largo em alguns terminais
	if j.paused.Load() {
		pauseIcon = " ▸ "
	}
	if j.failed != "" {
		pauseIcon = " ↻ "
	}
	pb := zone.Mark(fmt.Sprintf("sf:jp:%d", i), sAcc.Inherit(bg).Bold(true).Render(pauseIcon))
	x := zone.Mark(fmt.Sprintf("sf:jx:%d", i), sErr.Inherit(bg).Bold(true).Render(" × "))
	right := info + bg.Render(" ") + pb + x
	room := cols - lipgloss.Width(right) - 2
	label := j.label
	if r := []rune(label); len(r) > room && room > 1 {
		label = string(r[:room-1]) + "…"
	}
	st := sFg.Inherit(bg)
	if sel {
		st = st.Foreground(cAcc).Bold(true)
	}
	gap := max(1, cols-lipgloss.Width(right)-1-lipgloss.Width(label))
	return zone.Mark(fmt.Sprintf("sf:j:%d", i), bg.Render(" ")+st.Render(label)+bg.Render(strings.Repeat(" ", gap))+right)
}

// viewPane desenha um painel: título com o caminho e h-1 linhas de arquivos.
func (m Model) viewPane(v *sftpView, i, w, h int) []string {
	p := v.panes[i]
	name := [2]string{" Local ", " Remoto "}[i]
	title := lipgloss.NewStyle().Foreground(cDim).Bold(true).Render(name)
	if i == v.act {
		title = lipgloss.NewStyle().Background(cAcc).Foreground(cDark).Bold(true).Render(name)
	}
	lines := make([]string, 0, h)
	if p == nil {
		msg := "conectando…"
		if !v.connecting {
			msg = "sem conexão"
		}
		lines = append(lines, title, sDim.Render(" "+msg))
	} else {
		dir := p.dir
		if p.err != "" {
			dir = p.err
		}
		room := w - lipgloss.Width(title) - 1
		if r := []rune(dir); len(r) > room && room > 1 {
			dir = "…" + string(r[len(r)-room+1:])
		}
		st := sFg
		if p.err != "" {
			st = sErr
		}
		lines = append(lines, title+" "+st.Render(dir))
		off := p.visibleOff(h - 1)
		for j := off; j < min(len(p.items), off+h-1); j++ {
			lines = append(lines, zone.Mark(fmt.Sprintf("sf:%d:%d", i, j), m.viewItem(p.items[j], w, j == p.sel, i == v.act)))
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for k := range lines {
		lines[k] = lipgloss.NewStyle().Width(w).MaxWidth(w).Render(lines[k])
	}
	// a zona do painel inteiro (pra rodinha) envolve as linhas de arquivos
	body := zone.Mark(fmt.Sprintf("sf:p:%d", i), strings.Join(lines[1:], "\n"))
	return append(lines[:1], strings.Split(body, "\n")...)
}

func (m Model) viewItem(it sfItem, w int, sel, active bool) string {
	name, st := it.name, sFg
	size := ""
	switch {
	case it.name == "..":
		st = sDim
	case it.dir:
		name, st = it.name+"/", sAcc
	default:
		size = humanSize(it.size)
	}
	room := w - 2 - len(size) - 1
	if r := []rune(name); len(r) > room && room > 1 {
		name = string(r[:room-1]) + "…"
	}
	gap := max(1, w-1-lipgloss.Width(name)-len(size)-1)
	if sel {
		bg := lipgloss.NewStyle().Background(cSel)
		if active {
			st = st.Bold(true)
		}
		return bg.Render(" ") + st.Background(cSel).Render(name) + bg.Render(strings.Repeat(" ", gap)) + sDim.Background(cSel).Render(size) + bg.Render(" ")
	}
	return " " + st.Render(name) + strings.Repeat(" ", gap) + sDim.Render(size) + " "
}
