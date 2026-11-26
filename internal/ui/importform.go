package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
}

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
	return im
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
		im.err = ""
		return nil, false
	}
	// o mtsh captura o mouse, então o botão direito não chega no terminal pra colar
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Button == tea.MouseButtonRight && mm.Action == tea.MouseActionPress {
		return importPaste, false
	}
	if mm, ok := isLeftClick(msg); ok {
		switch {
		case clicked("m:close", mm), clicked("m:cancel", mm):
			return nil, true
		case clicked("m:ok", mm):
			return nil, im.submit(m)
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
			return nil, im.submit(m)
		case "tab", "down":
			im.setFocus(im.focus + 1)
			return nil, false
		case "shift+tab", "up":
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
	var cmd tea.Cmd
	im.path, cmd = im.path.Update(msg)
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
		"",
	}
	if im.err != "" {
		rows = append(rows, sErr.Render("  "+im.err))
	} else {
		rows = append(rows, sFaint.Render("  ctrl+v ou botão direito cola · tab vai pros botões · enter importar"))
	}
	btns := button("m:cancel", " Cancelar ", false, im.focus == 1) + "  " + button("m:ok", " Importar ", true, im.focus == 2)
	rows = append(rows, "", lipgloss.PlaceHorizontal(64, lipgloss.Right, btns), "")
	return dialog("Importar servidores", 70, strings.Join(rows, "\n"))
}
