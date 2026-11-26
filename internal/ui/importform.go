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

// importModal pede o arquivo exportado pelo electerm e importa os servidores.
type importModal struct {
	path textinput.Model
	err  string
}

func newImportModal() *importModal {
	im := &importModal{path: newInput("~/Downloads/electerm-all-data.json", 50)}
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
	if pm, ok := msg.(importPastedMsg); ok {
		if pm.err != nil {
			im.err = "Não deu pra ler a área de transferência: " + pm.err.Error()
			return nil, false
		}
		line, _, _ := strings.Cut(strings.TrimSpace(pm.text), "\n")
		im.path.SetValue(strings.TrimSpace(line))
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
			return nil, im.submit(m)
		case "ctrl+v":
			// o ctrl+v do textinput não alcança a área de transferência do Windows no WSL
			return importPaste, false
		}
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
	items, skipped, err := importer.Electerm(data)
	if err != nil {
		im.err = err.Error()
		return false
	}
	added, existing, err := m.importItems(items)
	if err != nil {
		im.err = err.Error()
		return false
	}
	msg := fmt.Sprintf("%d servidores importados", added)
	if existing > 0 {
		msg += fmt.Sprintf(" · %d já existiam", existing)
	}
	if skipped > 0 {
		msg += fmt.Sprintf(" · %d ignorados (não são SSH)", skipped)
	}
	m.say(msg, false)
	return true
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
	rows := []string{
		sFg.Render("Traz pastas, servidores e senhas do JSON exportado pelo electerm."),
		sFaint.Render("Servidores com nome já cadastrado ficam de fora."),
		"",
		fieldRow("Arquivo", true, im.path.View()),
		"",
	}
	if im.err != "" {
		rows = append(rows, sErr.Render("  "+im.err))
	} else {
		rows = append(rows, sFaint.Render("  ctrl+v ou botão direito cola o caminho · enter importar"))
	}
	btns := button("m:cancel", " Cancelar ", false, false) + "  " + button("m:ok", " Importar ", true, false)
	rows = append(rows, "", lipgloss.PlaceHorizontal(64, lipgloss.Right, btns), "")
	return dialog("Importar do electerm", 70, strings.Join(rows, "\n"))
}
