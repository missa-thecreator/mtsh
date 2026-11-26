package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/clipboard"
	"mtsh/internal/config"
	"mtsh/internal/importer"
)

// importModal pede o arquivo exportado pelo electerm ou pelo MobaXterm e importa os servidores.
type importModal struct {
	path textinput.Model
	err  string

	result []string // resumo depois de importar (a janela vira o resumo)
	focus  int      // 0 campo do arquivo, 1 Cancelar, 2 Importar

	// navegador: lista a pasta do caminho digitado, filtrada pelo pedaço depois da última "/"
	dir     string      // pasta lida (já expandida)
	all     []fileEntry // conteúdo de dir
	entries []fileEntry // o que aparece (com ".." e o filtro)
	sel     int         // item destacado; -1 = nenhum
	off     int         // primeira linha visível
}

type fileEntry struct {
	name string
	dir  bool
}

const browseRows = 8

func (im *importModal) setFocus(i int) {
	im.focus = (i%3 + 3) % 3
	if im.focus == 0 {
		im.path.Focus()
	} else {
		im.path.Blur()
	}
}

func newImportModal() *importModal {
	im := &importModal{path: newInput("~/Downloads/sessoes.mxtsessions", 46)}
	im.path.CharLimit = 1024
	im.path.Focus()
	im.path.SetValue("~/")
	im.path.CursorEnd()
	im.browse()
	return im
}

// browse atualiza a lista a partir do caminho digitado.
func (im *importModal) browse() {
	v := im.path.Value()
	i := strings.LastIndex(v, "/")
	// shortcut: caminho do Windows (C:\...) não é navegado, só importado; dá pra converter com winPath se precisar
	if i < 0 || winDrive.MatchString(v) {
		im.dir, im.all, im.entries, im.sel, im.off = "", nil, nil, -1, 0
		return
	}
	dir := config.ExpandHome(v[:i+1])
	if dir != im.dir {
		im.dir, im.all = dir, readDir(dir)
	}
	prefix := strings.ToLower(v[i+1:])
	im.entries = nil
	if prefix == "" && filepath.Clean(dir) != "/" {
		im.entries = append(im.entries, fileEntry{"..", true})
	}
	for _, e := range im.all {
		if strings.HasPrefix(strings.ToLower(e.name), prefix) {
			im.entries = append(im.entries, e)
		}
	}
	im.sel, im.off = -1, 0
}

// readDir lista a pasta com as subpastas primeiro; erro vira lista vazia.
func readDir(dir string) []fileEntry {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]fileEntry, 0, len(des))
	for _, d := range des {
		isDir := d.IsDir()
		if d.Type()&os.ModeSymlink != 0 { // link pra pasta também é pasta
			if st, err := os.Stat(filepath.Join(dir, d.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		out = append(out, fileEntry{d.Name(), isDir})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].dir && !out[b].dir })
	return out
}

// moveSel anda o destaque e rola a lista pra ele ficar visível.
func (im *importModal) moveSel(d int) {
	im.sel = max(-1, min(len(im.entries)-1, im.sel+d))
	if im.sel >= 0 && im.sel < im.off {
		im.off = im.sel
	}
	if im.sel >= im.off+browseRows {
		im.off = im.sel - browseRows + 1
	}
}

// open entra na pasta ou importa o arquivo do item i.
func (im *importModal) open(m *Model, i int) bool {
	e := im.entries[i]
	v := im.path.Value()
	base := v[:strings.LastIndex(v, "/")+1]
	switch {
	case e.name == "..":
		up := filepath.Dir(filepath.Clean(im.dir))
		im.path.SetValue(strings.TrimSuffix(up, "/") + "/")
	case e.dir:
		im.path.SetValue(base + e.name + "/")
	default:
		im.path.SetValue(base + e.name)
		im.path.CursorEnd()
		return im.submit(m)
	}
	im.path.CursorEnd()
	im.browse()
	return false
}

// importPastedMsg traz o texto da área de transferência pro campo do arquivo.
type importPastedMsg struct {
	text string
	err  error
}

func importPaste() tea.Msg {
	text, err := clipboard.Paste()
	return importPastedMsg{text, err}
}

func (im *importModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if im.result != nil {
		if _, ok := isLeftClick(msg); ok {
			return nil, true
		}
		if _, ok := msg.(tea.KeyMsg); ok {
			return nil, true
		}
		return nil, false
	}
	if pm, ok := msg.(importPastedMsg); ok {
		if pm.err != nil {
			im.err = "Não deu pra ler a área de transferência: " + pm.err.Error()
			return nil, false
		}
		line, _, _ := strings.Cut(strings.TrimSpace(pm.text), "\n")
		im.path.SetValue(strings.TrimSpace(line))
		im.setFocus(0)
		im.path.CursorEnd()
		im.browse()
		im.err = ""
		return nil, false
	}
	// o mtsh captura o mouse, então o botão direito não chega no terminal pra colar
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Button == tea.MouseButtonRight && mm.Action == tea.MouseActionPress {
		return importPaste, false
	}
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Action == tea.MouseActionPress &&
		(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown) {
		d := 3
		if mm.Button == tea.MouseButtonWheelUp {
			d = -3
		}
		im.off = max(0, min(len(im.entries)-browseRows, im.off+d))
		return nil, false
	}
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:close", mm), clicked("m:cancel", mm):
			return nil, true
		case clicked("m:ok", mm):
			if im.sel >= 0 && !im.entries[im.sel].dir {
				return nil, im.open(m, im.sel)
			}
			return nil, im.submit(m)
		}
		for i := im.off; i < min(len(im.entries), im.off+browseRows); i++ {
			if clicked(fmt.Sprintf("imp:%d", i), mm) {
				im.err = ""
				im.setFocus(0)
				// pasta abre no clique; arquivo destaca no primeiro e importa no segundo
				if im.entries[i].dir || im.sel == i {
					return nil, im.open(m, i)
				}
				im.sel = i
				return nil, false
			}
		}
		return nil, false
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		im.err = ""
		switch k.String() {
		case "esc":
			return nil, true
		case "enter":
			if im.focus == 1 {
				return nil, true // Cancelar
			}
			if im.sel >= 0 {
				return nil, im.open(m, im.sel)
			}
			return nil, im.submit(m)
		case "down", "up", "pgdown", "pgup":
			if im.focus == 0 { // no campo, as setas andam pela lista
				im.moveSel(map[string]int{"down": 1, "up": -1, "pgdown": browseRows, "pgup": -browseRows}[k.String()])
				return nil, false
			}
			if k.String() == "down" {
				im.setFocus(im.focus + 1)
			} else if k.String() == "up" {
				im.setFocus(im.focus - 1)
			}
			return nil, false
		case "tab":
			im.setFocus(im.focus + 1)
			return nil, false
		case "shift+tab":
			im.setFocus(im.focus - 1)
			return nil, false
		case "left", "right":
			if im.focus != 0 { // nos botões, troca entre Cancelar e Importar
				im.setFocus(3 - im.focus)
				return nil, false
			}
		case "ctrl+v":
			// o ctrl+v do textinput não alcança a área de transferência do Windows no WSL
			return importPaste, false
		}
	}
	if im.focus != 0 {
		return nil, false // nos botões, as letras não vão pro campo
	}
	before := im.path.Value()
	var cmd tea.Cmd
	im.path, cmd = im.path.Update(msg)
	if im.path.Value() != before {
		im.browse()
	}
	return cmd, false
}

func (im *importModal) submit(m *Model) bool {
	// arrastar o arquivo pro terminal costuma colar o caminho entre aspas
	p := strings.Trim(strings.TrimSpace(im.path.Value()), `"'`)
	if p == "" {
		im.err = "Informe o caminho do arquivo."
		return false
	}
	data, err := os.ReadFile(config.ExpandHome(winPath(p)))
	if err != nil {
		im.err = "Não deu pra ler o arquivo: " + err.Error()
		return false
	}
	items, skipped, source, err := importer.Detect(data)
	if err != nil {
		im.err = err.Error()
		return false
	}
	missingKeys, ppkKeys := loadKeyFiles(items)
	added, existing, err := m.importItems(items)
	if err != nil {
		im.err = err.Error()
		return false
	}
	m.say(fmt.Sprintf("%s importado%s do %s", plural(added, "servidor", "servidores"), pluralS(added), source), false)
	im.result = []string{sOk.Render("✓ " + plural(added, "servidor importado", "servidores importados") + " do " + source)}
	note := func(n int, text string) {
		if n > 0 {
			im.result = append(im.result, text)
		}
	}
	note(existing, sDim.Render("· "+plural(existing, "já existia (mesmo nome)", "já existiam (mesmo nome)")))
	note(skipped, sDim.Render("· "+plural(skipped, "ignorado: não é SSH (RDP, VNC…)", "ignorados: não são SSH (RDP, VNC…)")))
	note(ppkKeys, sAcc.Render("! "+plural(ppkKeys, "chave .ppk do PuTTY", "chaves .ppk do PuTTY")+": converta com o puttygen"))
	note(missingKeys, sAcc.Render("! "+plural(missingKeys, "chave não encontrada: o servidor ficou", "chaves não encontradas: os servidores ficaram")+" como automático"))
	if source == "MobaXterm" && added > 0 {
		im.result = append(im.result, sDim.Render("· senhas não vêm no arquivo: o ssh pergunta ao conectar"))
	}
	return false // continua aberta, mostrando o resumo
}

// plural escreve "1 servidor" ou "3 servidores".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// loadKeyFiles lê as chaves que o arquivo importado só referencia pelo caminho
// (MobaXterm grava o caminho do Windows; no WSL ele vira /mnt/c/...). Quem não
// tiver a chave legível fica como "automático".
func loadKeyFiles(items []importer.Item) (missing, ppk int) {
	for i := range items {
		it := &items[i]
		if it.Key != "" || it.KeyPath == "" {
			continue
		}
		data, err := os.ReadFile(config.ExpandHome(winPath(it.KeyPath)))
		switch {
		case err != nil:
			missing++
		case strings.HasPrefix(strings.TrimSpace(string(data)), "PuTTY-User-Key-File"):
			ppk++ // formato do PuTTY: o ssh não entende
		case strings.Contains(string(data), "PRIVATE KEY"):
			it.Key = string(data)
			it.Host.Auth = config.AuthKey
		default:
			missing++
		}
	}
	return missing, ppk
}

// importItems junta os servidores importados na config. Quem tem o mesmo nome
// de um servidor já cadastrado fica de fora, então importar de novo não duplica.
func (m *Model) importItems(items []importer.Item) (added, existing int, err error) {
	have := map[string]bool{}
	for _, h := range m.cfg.Hosts {
		have[h.Name] = true
	}
	keysDir := config.KeysDir(m.cfgPath)
	var fresh []importer.Item
	for _, it := range items {
		if have[it.Host.Name] {
			existing++
			continue
		}
		if it.Key != "" {
			if err := os.MkdirAll(keysDir, 0o700); err != nil {
				return 0, 0, err
			}
			dest := filepath.Join(keysDir, strings.ReplaceAll(it.Host.Name, "/", "-"))
			data := strings.TrimSpace(strings.ReplaceAll(it.Key, "\r\n", "\n")) + "\n"
			if err := os.WriteFile(dest, []byte(data), 0o600); err != nil {
				return 0, 0, fmt.Errorf("não deu pra salvar a chave de %s: %w", it.Host.Name, err)
			}
			it.Host.Key = dest
		}
		fresh = append(fresh, it)
	}
	for _, it := range fresh {
		m.cfg.Hosts = append(m.cfg.Hosts, it.Host)
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Hosts = m.cfg.Hosts[:len(m.cfg.Hosts)-len(fresh)]
		return 0, 0, fmt.Errorf("erro ao salvar: %w", err)
	}
	for _, it := range fresh {
		if it.Password != "" {
			if err := m.vault.Set(it.Host.Name, it.Password); err != nil {
				return len(fresh), existing, fmt.Errorf("servidores importados, mas a senha de %s não foi salva: %w", it.Host.Name, err)
			}
		}
		for p := it.Host.Folder; p != ""; p = config.Parent(p) {
			m.open[p] = true
		}
	}
	m.query, m.ctx, m.cardSel, m.gridOff = "", "", 0, 0
	m.selectTreeFolder("")
	return len(fresh), existing, nil
}

var winDrive = regexp.MustCompile(`^([A-Za-z]:\\|\\\\)`)

// winPath converte "C:\Users\..." ou "\\wsl.localhost\..." (o "Copiar como caminho" do Windows) pro
// caminho do WSL. Fora do WSL, ou se o wslpath falhar, devolve p como veio.
func winPath(p string) string {
	if !winDrive.MatchString(p) {
		return p
	}
	out, err := exec.Command("wslpath", "-u", p).Output()
	if err != nil {
		return p
	}
	return strings.TrimSpace(string(out))
}

func (im *importModal) view(m *Model) string {
	if im.result != nil {
		rows := append([]string{}, im.result...)
		rows = append(rows, "", lipgloss.PlaceHorizontal(64, lipgloss.Right, button("m:ok", " Fechar ", true, true)), "")
		return dialog("Importação concluída", 70, strings.Join(rows, "\n"))
	}
	rows := []string{
		sFg.Render("Traz pastas e servidores de:"),
		sDim.Render("  · MobaXterm: o .mxtsessions exportado ou o MobaXterm.ini"),
		sDim.Render("  · electerm: o JSON \"all data\", com as senhas"),
		sFaint.Render("Servidores com nome já cadastrado ficam de fora."),
		"",
		fieldRow("Arquivo", im.focus == 0, fieldBox(im.path.View(), im.path.Width)),
		im.viewList(),
		"",
	}
	if im.err != "" {
		rows = append(rows, sErr.Render("  "+im.err))
	} else {
		rows = append(rows, sFaint.Render("  ↑↓ escolhe · enter abre a pasta ou importa · ctrl+v cola · tab botões"))
	}
	btns := button("m:cancel", " Cancelar ", false, im.focus == 1) + "  " + button("m:ok", " Importar ", true, im.focus == 2)
	rows = append(rows, "", lipgloss.PlaceHorizontal(64, lipgloss.Right, btns), "")
	return dialog("Importar servidores", 70, strings.Join(rows, "\n"))
}

// viewList desenha a lista da pasta embaixo do campo, sempre com browseRows linhas.
func (im *importModal) viewList() string {
	const indent, w = 13, 48
	pad := strings.Repeat(" ", indent)
	lines := make([]string, 0, browseRows)
	if len(im.entries) == 0 {
		lines = append(lines, pad+sFaint.Render(" nada aqui"))
	}
	for i := im.off; i < min(len(im.entries), im.off+browseRows); i++ {
		e := im.entries[i]
		name, st := e.name, sFg
		if e.dir {
			name, st = e.name+"/", sAcc
		}
		if e.name == ".." {
			st = sDim
		}
		label := " " + name
		if r := []rune(label); len(r) > w {
			label = string(r[:w-1]) + "…"
		}
		if i == im.sel {
			st = st.Background(cSel).Bold(true)
		}
		lines = append(lines, pad+zone.Mark(fmt.Sprintf("imp:%d", i), st.Width(w).Render(label)))
	}
	for len(lines) < browseRows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
