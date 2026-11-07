// Package fonts instala as fontes que desenham os logos das distros e
// configura o terminal pra usar elas, sem mudar a aparência do texto:
//
//   - Windows Terminal aceita uma fonte só, então troca "Cascadia Mono" (a
//     padrão) por "Cascadia Mono NF": a mesma fonte, com os logos.
//   - VS Code aceita lista de fontes, então ganha "Symbols Nerd Font Mono"
//     como reserva, mantendo a fonte que já usava.
//
// As fontes vão embutidas no binário: Cascadia (SIL OFL, LICENSE-cascadia) e
// Symbols Nerd Font (MIT, LICENSE-nerd-fonts).
package fonts

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

//go:embed SymbolsNerdFontMono-Regular.ttf
var symbolsFont []byte

//go:embed CascadiaMonoNF.ttf
var cascadiaFont []byte

const (
	fontFile   = "SymbolsNerdFontMono-Regular.ttf"
	fontFamily = "Symbols Nerd Font Mono"

	cascadiaFile   = "CascadiaMonoNF.ttf"
	cascadiaFamily = "Cascadia Mono NF"
	cascadiaPlain  = "Cascadia Mono" // padrão do Windows Terminal
)

// Install instala a fonte e configura os terminais que encontrar.
// Vai escrevendo o que fez em out.
func Install(out io.Writer) error {
	switch {
	case isWSL():
		return installWSL(out)
	case runtime.GOOS == "darwin":
		home, _ := os.UserHomeDir()
		return installUnix(out, filepath.Join(home, "Library", "Fonts"))
	default:
		return installUnix(out, linuxFontsDir())
	}
}

func isWSL() bool {
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		return true
	}
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// ---- WSL: a tela é desenhada pelo Windows, então a fonte vai pro Windows ----

func installWSL(out io.Writer) error {
	local, err := winFolder("LocalApplicationData")
	if err != nil {
		return err
	}
	roaming, err := winFolder("ApplicationData")
	if err != nil {
		return err
	}

	// 1. fontes na pasta de fontes do usuário (não precisa de administrador)
	dir := filepath.Join(local, "Microsoft", "Windows", "Fonts")
	for _, f := range []struct {
		file, family string
		data         []byte
	}{{fontFile, fontFamily, symbolsFont}, {cascadiaFile, cascadiaFamily, cascadiaFont}} {
		if err := installWindowsFont(dir, f.file, f.family, f.data); err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ fonte %s instalada no Windows\n", f.family)
	}

	// 2. terminais
	configured := 0
	wt, _ := filepath.Glob(filepath.Join(local, "Packages", "Microsoft.WindowsTerminal*", "LocalState", "settings.json"))
	wt = append(wt, filepath.Join(local, "Microsoft", "Windows Terminal", "settings.json")) // versão sem loja
	for _, p := range wt {
		if ok := report(out, "Windows Terminal", p, ConfigureWindowsTerminal(p)); ok {
			configured++
		}
	}
	vs := filepath.Join(roaming, "Code", "User", "settings.json")
	if ok := report(out, "VS Code", vs, ConfigureVSCode(vs)); ok {
		configured++
	}
	if configured == 0 {
		fmt.Fprintf(out, "\nNão achei terminal pra configurar. Coloque \"%s\" como fonte de reserva no seu terminal.\n", fontFamily)
	}
	fmt.Fprintln(out, "\nFeche e abra o terminal pra os logos aparecerem.")
	return nil
}

// report escreve o resultado de configurar um terminal; devolve se configurou.
func report(out io.Writer, name, path string, err error) bool {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false
	case errors.Is(err, errAlready):
		fmt.Fprintf(out, "✓ %s já estava configurado\n", name)
		return true
	case errors.As(err, new(CustomFontError)):
		fmt.Fprintf(out, "! %s %v\n", name, err)
		return false
	case err != nil:
		fmt.Fprintf(out, "✗ %s: %v\n  coloque \"%s\" como fonte de reserva na mão\n", name, err, fontFamily)
		return false
	}
	fmt.Fprintf(out, "✓ %s configurado (cópia do original em %s.bak-mtsh)\n", name, filepath.Base(path))
	return true
}

// installWindowsFont copia a fonte e registra no usuário do Windows.
func installWindowsFont(dir, file, family string, data []byte) error {
	dest, err := writeFont(dir, file, data)
	if err != nil {
		return err
	}
	winDest, err := wslpath("-w", dest)
	if err != nil {
		return err
	}
	reg := exec.Command("reg.exe", "add", `HKCU\Software\Microsoft\Windows NT\CurrentVersion\Fonts`,
		"/v", family+" (TrueType)", "/t", "REG_SZ", "/d", winDest, "/f")
	if b, err := reg.CombinedOutput(); err != nil {
		return fmt.Errorf("não deu pra registrar a fonte %s no Windows: %v: %s", family, err, strings.TrimSpace(string(b)))
	}
	return nil
}

func winFolder(name string) (string, error) {
	b, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[Environment]::GetFolderPath('"+name+"')").Output()
	if err != nil {
		return "", fmt.Errorf("não consegui falar com o Windows (powershell.exe): %w", err)
	}
	return wslpath("-u", strings.TrimSpace(string(b)))
}

func wslpath(flag, p string) (string, error) {
	b, err := exec.Command("wslpath", flag, p).Output()
	if err != nil {
		return "", fmt.Errorf("wslpath %s %s: %w", flag, p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ---- Linux / macOS ----

// fontconfigRule faz a Symbols Nerd Font Mono servir de reserva pra qualquer
// fonte: o terminal acha os logos nela sem precisar de configuração.
const fontconfigRule = `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<!-- Criado pelo mtsh: usa a Symbols Nerd Font Mono como reserva pros logos
     das distros, em qualquer fonte. Pra desfazer, apague este arquivo. -->
<fontconfig>
  <match target="pattern">
    <edit name="family" mode="append">
      <string>Symbols Nerd Font Mono</string>
    </edit>
  </match>
</fontconfig>
`

const fontconfigFile = "99-mtsh-symbols.conf"

func xdgDir(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func linuxFontsDir() string { return filepath.Join(xdgDir("XDG_DATA_HOME", ".local/share"), "fonts") }

// FontconfigPath é a regra que o mtsh cria (pra desinstalar, é só apagar).
func FontconfigPath() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "fontconfig", "conf.d", fontconfigFile)
}

// logoFonts pergunta ao fontconfig quais fontes têm o logo do Ubuntu (U+F31B).
// ok=false quando não dá pra perguntar (sem fc-list).
func logoFonts() (families []string, ok bool) {
	out, err := exec.Command("fc-list", ":charset=f31b", "family").Output()
	if err != nil {
		return nil, false
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			families = append(families, l)
		}
	}
	return families, true
}

func installUnix(out io.Writer, dir string) error {
	if runtime.GOOS == "darwin" {
		dest, err := writeFont(dir, fontFile, symbolsFont)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ fonte instalada em %s\n\n", dest)
		fmt.Fprintf(out, "Se o seu terminal não usar a fonte sozinho, coloque \"%s\" como fonte de reserva.\n", fontFamily)
		return nil
	}

	// já tem alguma Nerd Font com os logos? então não precisa instalar nada
	if fams, ok := logoFonts(); ok && len(fams) > 0 && !anyContains(fams, fontFamily) {
		fmt.Fprintf(out, "✓ você já tem uma fonte com os logos (%s); nada a instalar\n", fams[0])
		terminalTips(out)
		return nil
	}

	dest, err := writeFont(dir, fontFile, symbolsFont)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ fonte instalada em %s\n", dest)

	conf := FontconfigPath()
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return err
	}
	if cur, err := os.ReadFile(conf); err != nil || string(cur) != fontconfigRule {
		if err := os.WriteFile(conf, []byte(fontconfigRule), 0o644); err != nil {
			return fmt.Errorf("não deu pra criar a regra do fontconfig: %w", err)
		}
	}
	fmt.Fprintf(out, "✓ fonte de reserva configurada (%s)\n", conf)

	if _, err := exec.LookPath("fc-cache"); err == nil {
		exec.Command("fc-cache", "-f", dir).Run()
	}
	if fams, ok := logoFonts(); ok && len(fams) == 0 {
		fmt.Fprintln(out, "! o fontconfig ainda não enxerga a fonte; tente fechar e abrir a sessão")
	}
	terminalTips(out)
	return nil
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// InSSH diz se o mtsh está rodando num servidor acessado por SSH.
func InSSH() bool { return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" }

// terminalTips dá a dica certa pro terminal em uso.
func terminalTips(out io.Writer) {
	fmt.Fprintln(out)
	switch {
	case InSSH():
		fmt.Fprintln(out, "! Você está numa sessão SSH. Quem desenha os logos é o terminal do seu")
		fmt.Fprintln(out, "  computador, então a fonte precisa estar nele: rode mtsh --instalar-icones")
		fmt.Fprintln(out, "  lá, ou escolha \"Bolinha na cor da distro\" nos Ajustes.")
	case os.Getenv("WEZTERM_EXECUTABLE") != "" || os.Getenv("TERM_PROGRAM") == "WezTerm":
		fmt.Fprintln(out, "O WezTerm já traz os ícones; os logos devem aparecer direto.")
	case os.Getenv("KITTY_WINDOW_ID") != "":
		fmt.Fprintln(out, "kitty: feche e abra o terminal. Se algum logo não aparecer, adicione no kitty.conf:")
		fmt.Fprintf(out, "  symbol_map U+E000-U+F8FF %s\n", fontFamily)
	default:
		fmt.Fprintln(out, "Feche e abra o terminal pra os logos aparecerem.")
	}
}

func writeFont(dir, file string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("não deu pra criar %s: %w", dir, err)
	}
	dest := filepath.Join(dir, file)
	// já instalada? (depois de instalada o Windows trava o arquivo, então
	// tentar sobrescrever daria "permission denied")
	if cur, err := os.ReadFile(dest); err == nil && bytes.Equal(cur, data) {
		return dest, nil
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", fmt.Errorf("não deu pra substituir a fonte em %s (o sistema está usando ela); "+
				"feche os terminais e tente de novo: %w", dir, err)
		}
		return "", fmt.Errorf("não deu pra copiar a fonte: %w", err)
	}
	return dest, nil
}

// ---- configuração dos terminais ----

var errAlready = errors.New("já configurado")

// withFallback acrescenta a fonte de símbolos numa lista de fontes.
func withFallback(current, def, quote string) string {
	if strings.TrimSpace(current) == "" {
		current = def
	}
	return current + ", " + quote + fontFamily + quote
}

// CustomFontError: o Windows Terminal usa uma fonte escolhida pela pessoa;
// não trocamos, só avisamos.
type CustomFontError struct{ Face string }

func (e CustomFontError) Error() string {
	return fmt.Sprintf("usa a fonte \"%s\", que não tem os logos; troque por \"%s\" (já instalada) ou outra Nerd Font", e.Face, cascadiaFamily)
}

// wtFace decide a nova fonte de um perfil do Windows Terminal.
// Versões antigas do mtsh gravavam uma lista com vírgula, que o Windows
// Terminal não entende; ela é corrigida aqui.
func wtFace(cur string) (face string, change bool, err error) {
	cur = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cur), ", "+fontFamily))
	switch {
	case cur == "" || cur == cascadiaPlain:
		return cascadiaFamily, true, nil
	case strings.Contains(cur, " NF") || strings.Contains(cur, "Nerd Font"):
		return cur, false, nil // já tem os logos
	}
	return cur, false, CustomFontError{cur}
}

// ConfigureWindowsTerminal troca "Cascadia Mono" (a padrão) por
// "Cascadia Mono NF" na fonte padrão dos perfis e nos perfis com fonte própria.
func ConfigureWindowsTerminal(path string) error {
	return editJSON(path, func(js string) (string, error) {
		type field struct{ path, cur string }
		fields := []field{{"profiles.defaults.font.face", gjson.Get(js, "profiles.defaults.font.face").String()}}
		for i, p := range gjson.Get(js, "profiles.list").Array() {
			if face := p.Get("font.face").String(); face != "" {
				fields = append(fields, field{fmt.Sprintf("profiles.list.%d.font.face", i), face})
			}
		}
		changed := false
		var custom error
		for _, f := range fields {
			face, change, err := wtFace(f.cur)
			if err != nil {
				custom = err
				continue
			}
			// o original pode ter vindo com a lista quebrada: grava mesmo assim
			if change || face != f.cur {
				var serr error
				if js, serr = sjson.Set(js, f.path, face); serr != nil {
					return "", serr
				}
				changed = true
			}
		}
		switch {
		case changed:
			return js, nil
		case custom != nil:
			return "", custom
		}
		return "", errAlready
	})
}

// ConfigureVSCode coloca a fonte de símbolos como reserva no terminal do VS Code,
// mantendo a fonte que ele já usava (a do terminal, senão a do editor).
func ConfigureVSCode(path string) error {
	return editJSON(path, func(js string) (string, error) {
		if strings.Contains(js, fontFamily) {
			return "", errAlready
		}
		cur := gjson.Get(js, `terminal\.integrated\.fontFamily`).String()
		if cur == "" {
			cur = gjson.Get(js, `editor\.fontFamily`).String()
		}
		// "monospace" é genérico e sempre "acha" uma fonte; a reserva tem que vir antes dele
		generic := ""
		if trimmed := strings.TrimSpace(cur); strings.HasSuffix(trimmed, ", monospace") {
			cur, generic = strings.TrimSuffix(trimmed, ", monospace"), ", monospace"
		}
		face := withFallback(cur, "Consolas, 'Courier New'", "'") + generic
		if cur == "" {
			face += ", monospace"
		}
		return sjson.Set(js, `terminal\.integrated\.fontFamily`, face)
	})
}

// editJSON altera um settings.json guardando uma cópia do original.
// Só mexe no campo da fonte; o resto do arquivo fica como estava.
func editJSON(path string, edit func(string) (string, error)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	js := strings.TrimPrefix(string(raw), "\uFEFF")
	if !gjson.Valid(js) {
		return errors.New("o arquivo tem comentários ou não é JSON válido; não mexi nele")
	}
	updated, err := edit(js)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".bak-mtsh", raw, 0o644); err != nil {
		return fmt.Errorf("não deu pra fazer a cópia de segurança: %w", err)
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// ---- verificação (sem mudar nada) ----

// Terminal é o estado de um terminal encontrado.
type Terminal struct {
	Name string
	OK   bool   // já mostra os logos
	Note string // explicação quando não está OK
}

// Status diz se os logos das distros vão aparecer.
type Status struct {
	FontInstalled bool
	Terminals     []Terminal
	Hint          string // observação pra mostrar nos Ajustes (não impede Ready)
	Err           error
}

// Ready: fonte instalada e todos os terminais encontrados configurados.
func (s Status) Ready() bool {
	if s.Err != nil || !s.FontInstalled {
		return false
	}
	for _, t := range s.Terminals {
		if !t.OK {
			return false
		}
	}
	return true
}

// Check confere a instalação sem alterar nada.
func Check() Status {
	if !isWSL() {
		return checkUnix()
	}

	local, err := winFolder("LocalApplicationData")
	if err != nil {
		return Status{Err: err}
	}
	roaming, err := winFolder("ApplicationData")
	if err != nil {
		return Status{Err: err}
	}
	dir := filepath.Join(local, "Microsoft", "Windows", "Fonts")
	st := Status{FontInstalled: sameFile(filepath.Join(dir, fontFile), symbolsFont) &&
		sameFile(filepath.Join(dir, cascadiaFile), cascadiaFont)}

	wt, _ := filepath.Glob(filepath.Join(local, "Packages", "Microsoft.WindowsTerminal*", "LocalState", "settings.json"))
	wt = append(wt, filepath.Join(local, "Microsoft", "Windows Terminal", "settings.json"))
	for _, p := range wt {
		js, err := readJSON(p)
		if err != nil {
			continue
		}
		t := Terminal{Name: "Windows Terminal", OK: true}
		faces := []string{gjson.Get(js, "profiles.defaults.font.face").String()}
		for _, pr := range gjson.Get(js, "profiles.list").Array() {
			if f := pr.Get("font.face").String(); f != "" {
				faces = append(faces, f)
			}
		}
		for _, f := range faces {
			face, change, err := wtFace(f)
			if err != nil {
				t.OK, t.Note = false, err.Error()
			} else if change || face != f {
				t.OK, t.Note = false, "ainda não configurado"
			}
		}
		st.Terminals = append(st.Terminals, t)
	}
	if js, err := readJSON(filepath.Join(roaming, "Code", "User", "settings.json")); err == nil {
		t := Terminal{Name: "VS Code", OK: strings.Contains(js, fontFamily)}
		if !t.OK {
			t.Note = "ainda não configurado"
		}
		st.Terminals = append(st.Terminals, t)
	}
	return st
}

func sameFile(path string, data []byte) bool {
	cur, err := os.ReadFile(path)
	return err == nil && bytes.Equal(cur, data)
}

func readJSON(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(raw), "\uFEFF"), nil
}

// checkUnix: Linux (pergunta ao fontconfig) e macOS (confere o arquivo).
func checkUnix() Status {
	st := Status{}
	if InSSH() {
		st.Hint = "Sessão SSH: os logos dependem da fonte do computador onde está o seu terminal."
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		st.FontInstalled = sameFile(filepath.Join(home, "Library", "Fonts", fontFile), symbolsFont)
		return st
	}
	if fams, ok := logoFonts(); ok {
		st.FontInstalled = len(fams) > 0
		if st.FontInstalled && !anyContains(fams, fontFamily) {
			st.Hint = "Usando a fonte " + fams[0] + ", que já tem os logos."
		}
		return st
	}
	st.FontInstalled = sameFile(filepath.Join(linuxFontsDir(), fontFile), symbolsFont)
	return st
}
