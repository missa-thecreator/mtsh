package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"mtsh/internal/config"
)

var authOptions = []struct{ id, label string }{
	{config.AuthPassword, " Senha "},
	{config.AuthKey, " Chave "},
	{config.AuthAuto, " Automático "},
}

var keyHeader = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)

// hostModal é a janela de novo servidor / editar servidor.
type hostModal struct {
	orig *config.Host // nil = novo

	name, addr, port, user, pass textinput.Model
	folder                       string
	auth                         string
	key                          string // chave colada (texto)
	keepKey                      bool   // editando: manter a chave que já existe
	hasSavedPass                 bool   // editando: já tem senha salva
	remember                     bool

	focus int
	err   string
}

func newHostModal(m *Model, orig *config.Host, folder string) *hostModal {
	h := &hostModal{
		name: newInput("meu-servidor", 34), addr: newInput("192.168.1.10 ou servidor.com", 26),
		port: newInput("22", 6), user: newInput("root", 34), pass: newInput("digite a senha", 34),
		folder: folder, auth: config.AuthKey, remember: true,
	}
	h.port.CharLimit = 5
	h.pass.EchoMode = textinput.EchoPassword
	h.pass.EchoCharacter = '•'
	if orig != nil {
		cp := *orig
		h.orig = &cp
		h.name.SetValue(orig.Name)
		h.addr.SetValue(orig.Host)
		h.user.SetValue(orig.User)
		if orig.Port != 0 {
			h.port.SetValue(strconv.Itoa(orig.Port))
		}
		h.folder = orig.Folder
		h.auth = orig.AuthType()
		h.keepKey = orig.Key != ""
		h.hasSavedPass = m.vault.Has(orig.Name)
		// "Lembrar" vem marcado: digitou a senha e salvou, ela fica salva
		h.remember = true
		if h.hasSavedPass {
			h.pass.Placeholder = "já salva · vazio mantém"
		}
	}
	h.setFocus(0)
	return h
}

func (h *hostModal) fields() []string {
	f := []string{"name", "addr", "port", "user", "folder", "auth"}
	switch h.auth {
	case config.AuthKey:
		f = append(f, "key")
	case config.AuthPassword:
		f = append(f, "pass", "remember")
	}
	return append(f, "cancel", "save")
}

func (h *hostModal) cur() string { return h.fields()[h.focus] }

func (h *hostModal) inputs() map[string]*textinput.Model {
	return map[string]*textinput.Model{"name": &h.name, "addr": &h.addr, "port": &h.port, "user": &h.user, "pass": &h.pass}
}

func (h *hostModal) setFocus(i int) {
	n := len(h.fields())
	h.focus = (i%n + n) % n
	for id, ti := range h.inputs() {
		if id == h.cur() {
			ti.Focus()
		} else {
			ti.Blur()
		}
	}
}

func (h *hostModal) focusID(id string) {
	for i, f := range h.fields() {
		if f == id {
			h.setFocus(i)
		}
	}
}

func (h *hostModal) keyValid() bool { return keyHeader.MatchString(h.key) }

// keyIncomplete: começou a colar mas ainda não chegou o fim (paste sem bracketed paste).
func (h *hostModal) keyIncomplete() bool {
	return strings.HasPrefix(strings.TrimSpace(h.key), "-----BEGIN") && !strings.Contains(h.key, "-----END")
}

func (h *hostModal) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := isLeftClick(msg); ok {
		return h.click(m, mm)
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	h.err = ""
	cur := h.cur()

	// área da chave: tudo que chega é colagem
	if cur == "key" {
		switch {
		case k.Paste:
			// colagem protegida (bracketed paste): chega tudo de uma vez
			h.key = strings.ReplaceAll(string(k.Runes), "\r", "\n")
			h.keepKey = false
			return nil, false
		case k.Type == tea.KeyEnter && h.keyIncomplete():
			h.key += "\n"
			return nil, false
		case k.Type == tea.KeyRunes || k.Type == tea.KeySpace:
			// terminal sem bracketed paste: a chave chega em pedaços
			if h.key != "" || strings.HasPrefix(string(k.Runes), "-") {
				h.key += string(k.Runes)
				h.keepKey = false
			}
			return nil, false
		case k.Type == tea.KeyBackspace || k.Type == tea.KeyDelete:
			h.key = ""
			return nil, false
		}
	}

	switch k.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		h.setFocus(h.focus + 1)
		return nil, false
	case "shift+tab", "up":
		h.setFocus(h.focus - 1)
		return nil, false
	case "enter":
		if cur == "cancel" {
			return nil, true
		}
		return h.submit(m)
	case " ":
		if cur == "remember" {
			h.remember = !h.remember
			return nil, false
		}
	case "left", "right":
		d := 1
		if k.String() == "left" {
			d = -1
		}
		switch cur {
		case "folder":
			h.folder = cycle(append([]string{""}, m.cfg.Folders...), h.folder, d)
			return nil, false
		case "auth":
			ids := []string{config.AuthPassword, config.AuthKey, config.AuthAuto}
			h.auth = cycle(ids, h.auth, d)
			return nil, false
		case "cancel", "save":
			if cur == "cancel" {
				h.focusID("save")
			} else {
				h.focusID("cancel")
			}
			return nil, false
		}
	}

	if ti, ok := h.inputs()[cur]; ok {
		if cur == "port" && k.Type == tea.KeyRunes && strings.Trim(string(k.Runes), "0123456789") != "" {
			return nil, false
		}
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		return cmd, false
	}
	return nil, false
}

func (h *hostModal) click(m *Model, mm tea.MouseMsg) (tea.Cmd, bool) {
	switch {
	case clicked("m:close", mm), clicked("m:cancel", mm):
		return nil, true
	case clicked("m:ok", mm):
		return h.submit(m)
	case clicked("h:folder", mm):
		h.focusID("folder")
		h.folder = cycle(append([]string{""}, m.cfg.Folders...), h.folder, 1)
		return nil, false
	case clicked("h:remember", mm):
		h.focusID("remember")
		h.remember = !h.remember
		return nil, false
	}
	for _, o := range authOptions {
		if clicked("h:auth:"+o.id, mm) {
			h.auth = o.id
			h.focusID("auth")
			return nil, false
		}
	}
	for _, id := range []string{"name", "addr", "port", "user", "pass", "key"} {
		if clicked("h:"+id, mm) {
			h.focusID(id)
		}
	}
	return nil, false
}

// submit valida e pede pro Model salvar.
func (h *hostModal) submit(m *Model) (tea.Cmd, bool) {
	name := strings.TrimSpace(h.name.Value())
	addr := strings.TrimSpace(h.addr.Value())
	switch {
	case name == "":
		h.err = "Dá um nome pro servidor."
		h.focusID("name")
		return nil, false
	case strings.ContainsAny(name, "/\\"):
		h.err = "O nome não pode ter / ou \\."
		h.focusID("name")
		return nil, false
	case addr == "":
		h.err = "Falta o endereço (IP ou domínio)."
		h.focusID("addr")
		return nil, false
	}
	for _, x := range m.cfg.Hosts {
		if x.Name == name && (h.orig == nil || h.orig.Name != name) {
			h.err = "Já existe um servidor chamado " + name + "."
			h.focusID("name")
			return nil, false
		}
	}
	port := 0
	if p := strings.TrimSpace(h.port.Value()); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			h.err = "A porta tem que ser um número de 1 a 65535."
			h.focusID("port")
			return nil, false
		}
		port = n
	}
	if h.auth == config.AuthKey && !h.keepKey {
		if h.key == "" {
			h.err = "Cole a chave privada no campo Chave."
			h.focusID("key")
			return nil, false
		}
		if !h.keyValid() || h.keyIncomplete() {
			h.err = "Isso não parece uma chave privada completa."
			h.focusID("key")
			return nil, false
		}
	}
	savePass := h.auth == config.AuthPassword && h.remember && h.pass.Value() != ""

	host := config.Host{Name: name, Folder: h.folder, Host: addr, User: strings.TrimSpace(h.user.Value()), Port: port, Auth: h.auth}
	if h.orig != nil {
		host.Tags = h.orig.Tags
		// mantém a distro detectada (só some se o endereço mudar)
		if h.orig.Host == host.Host && h.orig.Port == host.Port {
			host.OS, host.OSLike, host.OSName = h.orig.OS, h.orig.OSLike, h.orig.OSName
		}
	}

	if err := m.saveHost(h, host, savePass); err != nil {
		h.err = err.Error()
		return nil, false
	}
	return nil, true
}

func (h *hostModal) view(m *Model) string {
	f := h.cur()
	in := func(id string, ti textinput.Model) string { return zone.Mark("h:"+id, ti.View()) }

	addrLine := in("addr", h.addr) + sDim.Render("  porta ") + in("port", h.port)
	folder := zone.Mark("h:folder", sFg.Render(folderLabel(h.folder))+sFaint.Render("  ◂ ▸"))

	var seg []string
	for _, o := range authOptions {
		st := sBtnGhost.Foreground(cDim)
		if h.auth == o.id {
			st = sBtnMain
		}
		seg = append(seg, zone.Mark("h:auth:"+o.id, st.Render(o.label)))
	}

	rows := []string{
		fieldRow("Nome", f == "name", in("name", h.name)),
		fieldRow("Endereço", f == "addr" || f == "port", addrLine),
		fieldRow("Usuário", f == "user", in("user", h.user)),
		fieldRow("Pasta", f == "folder", folder),
		"",
		fieldRow("Acesso", f == "auth", strings.Join(seg, " ")+sFaint.Render("  ◂ ▸")),
		"",
	}

	switch h.auth {
	case config.AuthKey:
		rows = append(rows, fieldRow("Chave", f == "key", zone.Mark("h:key", h.keyBox(f == "key"))))
	case config.AuthPassword:
		box := "[ ]"
		if h.remember {
			box = "[x]"
		}
		rem := zone.Mark("h:remember", lipgloss.NewStyle().Foreground(cDim).Render(box+" Lembrar a senha neste computador"))
		if f == "remember" {
			rem = zone.Mark("h:remember", sAcc.Render(box+" Lembrar a senha neste computador"))
		}
		rows = append(rows, fieldRow("Senha", f == "pass", in("pass", h.pass)), fieldRow("", false, rem))
		if !h.remember {
			rows = append(rows, fieldRow("", false, sFaint.Render("o ssh vai perguntar a senha ao conectar")))
		}
	default:
		rows = append(rows, fieldRow("", false, sDim.Render("Usa o que este computador já tem: ssh-agent,\nchaves em ~/.ssh e o ~/.ssh/config.")))
	}

	rows = append(rows, "")
	if h.err != "" {
		rows = append(rows, sErr.Render("  "+h.err))
	} else {
		rows = append(rows, sFaint.Render("  tab próximo · ◂ ▸ escolher · enter salvar · esc fechar"))
	}
	btns := button("m:cancel", " Cancelar ", false, f == "cancel") + "  " + button("m:ok", " Salvar ", true, f == "save")
	rows = append(rows, lipgloss.PlaceHorizontal(62, lipgloss.Right, btns), "")

	title := "Novo servidor"
	if h.orig != nil {
		title = "Editar " + h.orig.Name
	}
	return dialog(title, 68, strings.Join(rows, "\n"))
}

func (h *hostModal) keyBox(focused bool) string {
	var text string
	color := cFaint
	switch {
	case h.keepKey && h.key == "":
		text = sOk.Render("✓ usando a chave já salva") + "\n" + sDim.Render("cole outra aqui pra trocar")
		color = cOk
	case h.key == "":
		text = sDim.Render("Cole sua chave privada aqui") + "\n" + sFaint.Render("ctrl+shift+v ou botão direito")
	case h.keyIncomplete():
		text = sAcc.Render(fmt.Sprintf("recebendo… %d linhas", strings.Count(h.key, "\n")+1)) + "\n" + sFaint.Render("")
		color = cAcc
	case h.keyValid():
		text = sOk.Render("✓ Chave reconhecida") + "\n" + sDim.Render("backspace apaga")
		color = cOk
	default:
		text = sErr.Render("Isso não parece uma chave privada.") + "\n" + sDim.Render("backspace apaga e tenta de novo")
		color = cErr
	}
	if focused && h.key == "" && !h.keepKey {
		color = cAcc
	}
	return lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(color).
		Width(44).Padding(0, 1).Render(text)
}

// ---- gravação (no Model, porque mexe na config, nas chaves e nas senhas) ----

func (m *Model) saveHost(h *hostModal, host config.Host, savePass bool) error {
	keysDir := config.KeysDir(m.cfgPath)
	managed := func(p string) bool {
		return p != "" && filepath.Dir(config.ExpandHome(p)) == keysDir
	}
	oldName := ""
	if h.orig != nil {
		oldName = h.orig.Name
	}

	// chave
	if host.Auth == config.AuthKey {
		dest := filepath.Join(keysDir, host.Name)
		switch {
		case !h.keepKey:
			if err := os.MkdirAll(keysDir, 0o700); err != nil {
				return err
			}
			data := strings.TrimSpace(strings.ReplaceAll(h.key, "\r\n", "\n")) + "\n"
			if err := os.WriteFile(dest, []byte(data), 0o600); err != nil {
				return fmt.Errorf("não deu pra salvar a chave: %w", err)
			}
			host.Key = dest
		case managed(h.orig.Key) && oldName != host.Name:
			if err := os.Rename(config.ExpandHome(h.orig.Key), dest); err != nil {
				return fmt.Errorf("não deu pra renomear a chave: %w", err)
			}
			host.Key = dest
		default:
			host.Key = h.orig.Key
		}
	} else if h.orig != nil && managed(h.orig.Key) {
		os.Remove(config.ExpandHome(h.orig.Key))
	}

	// senha
	if oldName != "" && oldName != host.Name {
		if err := m.vault.Rename(oldName, host.Name); err != nil {
			return err
		}
	}
	switch {
	case host.Auth != config.AuthPassword || !h.remember:
		if err := m.vault.Delete(host.Name); err != nil {
			return err
		}
	case savePass:
		if err := m.vault.Set(host.Name, h.pass.Value()); err != nil {
			return err
		}
	}

	// config
	replaced := false
	for i := range m.cfg.Hosts {
		if h.orig != nil && m.cfg.Hosts[i].Name == oldName {
			m.cfg.Hosts[i] = host
			replaced = true
		}
	}
	if !replaced {
		m.cfg.Hosts = append(m.cfg.Hosts, host)
	}
	if !m.persist() {
		return nil
	}

	for p := host.Folder; p != ""; p = config.Parent(p) {
		m.open[p] = true
	}
	m.query, m.ctx, m.focus = "", host.Folder, focusGrid
	m.selectCard(host.Name)
	m.selectTreeFolder(host.Folder)
	if host.Auth == config.AuthPassword && !m.vault.Has(host.Name) {
		m.say(host.Name+" salvo · sem senha salva, o ssh vai perguntar ao conectar", false)
	} else {
		m.say(host.Name+" salvo", false)
	}
	return nil
}
