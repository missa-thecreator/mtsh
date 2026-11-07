package fonts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWindowsTerminal(t *testing.T) {
	orig := `{
    "defaultProfile": "{x}",
    "profiles": {
        "defaults": {},
        "list": [
            {"name": "Ubuntu"},
            {"name": "Antigo", "font": {"face": "Cascadia Mono"}},
            {"name": "Outro", "font": {"face": "Fira Code"}}
        ]
    }
}`
	p := writeTemp(t, orig)
	if err := ConfigureWindowsTerminal(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	js := string(b)
	if got := gjson.Get(js, "profiles.defaults.font.face").String(); got != "Cascadia Mono NF" {
		t.Errorf("defaults: %q", got)
	}
	if got := gjson.Get(js, "profiles.list.1.font.face").String(); got != "Cascadia Mono NF" {
		t.Errorf("perfil com Cascadia Mono: %q", got)
	}
	if got := gjson.Get(js, "profiles.list.2.font.face").String(); got != "Fira Code" {
		t.Errorf("fonte escolhida pela pessoa não pode mudar: %q", got)
	}
	if strings.Contains(js, ",") && strings.Contains(js, "Cascadia Mono NF,") {
		t.Error("o Windows Terminal não aceita lista de fontes")
	}
	if !strings.Contains(js, `"defaultProfile": "{x}"`) {
		t.Error("o resto do arquivo deveria ficar igual")
	}
	if bak, _ := os.ReadFile(p + ".bak-mtsh"); string(bak) != orig {
		t.Error("a cópia de segurança deveria ter o original")
	}
	// segunda vez: só sobra a fonte escolhida pela pessoa, que não muda
	if err := ConfigureWindowsTerminal(p); !errors.As(err, new(CustomFontError)) {
		t.Errorf("segunda vez: %v", err)
	}
	if bak, _ := os.ReadFile(p + ".bak-mtsh"); string(bak) != orig {
		t.Error("a segunda execução não pode sobrescrever a cópia")
	}
}

func TestWindowsTerminalCorrigeListaAntiga(t *testing.T) {
	p := writeTemp(t, `{"profiles": {"defaults": {"font":{"face":"Cascadia Mono, Symbols Nerd Font Mono"}}, "list": []}}`)
	if err := ConfigureWindowsTerminal(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if got := gjson.Get(string(b), "profiles.defaults.font.face").String(); got != "Cascadia Mono NF" {
		t.Errorf("lista antiga deveria virar Cascadia Mono NF: %q", got)
	}
	if err := ConfigureWindowsTerminal(p); !errors.Is(err, errAlready) {
		t.Errorf("já configurado: %v", err)
	}
}

func TestVSCode(t *testing.T) {
	p := writeTemp(t, `{"workbench.iconTheme": "material-icon-theme", "editor.fontFamily": "JetBrains Mono"}`)
	if err := ConfigureVSCode(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if got := gjson.Get(string(b), `terminal\.integrated\.fontFamily`).String(); got != "JetBrains Mono, 'Symbols Nerd Font Mono'" {
		t.Errorf("fontFamily: %q", got)
	}
}

func TestComentariosNaoMexe(t *testing.T) {
	orig := "{\n  // comentário\n  \"a\": 1\n}"
	p := writeTemp(t, orig)
	if err := ConfigureVSCode(p); err == nil {
		t.Error("arquivo com comentário deveria dar erro em vez de ser reescrito")
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Error("arquivo com comentário não pode ser alterado")
	}
}

func TestFonteEmbutida(t *testing.T) {
	if len(symbolsFont) < 1_000_000 || string(symbolsFont[:4]) != "\x00\x01\x00\x00" {
		t.Errorf("fonte embutida inválida (%d bytes)", len(symbolsFont))
	}
}

func TestVSCodePadrao(t *testing.T) {
	p := writeTemp(t, `{"workbench.iconTheme": "material-icon-theme"}`)
	if err := ConfigureVSCode(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if got := gjson.Get(string(b), `terminal\.integrated\.fontFamily`).String(); got != "Consolas, 'Courier New', 'Symbols Nerd Font Mono', monospace" {
		t.Errorf("fontFamily: %q", got)
	}
}

func TestFonteJaInstaladaNaoRegrava(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeFont(dir, fontFile, symbolsFont); err != nil {
		t.Fatal(err)
	}
	// simula o Windows travando o arquivo: sem permissão de escrita
	dest := filepath.Join(dir, fontFile)
	if err := os.Chmod(dest, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFont(dir, fontFile, symbolsFont); err != nil {
		t.Errorf("fonte idêntica já instalada não deveria dar erro: %v", err)
	}
}

// Instala de verdade numa "pasta pessoal" temporária e confere, pelo próprio
// fontconfig, que os logos passam a ser encontrados.
func TestInstalarLinux(t *testing.T) {
	if _, err := exec.LookPath("fc-list"); err != nil {
		t.Skip("sem fontconfig")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	if fams, _ := logoFonts(); len(fams) > 0 {
		t.Skipf("o sistema já tem fonte com os logos (%v)", fams)
	}
	if st := checkUnix(); st.FontInstalled {
		t.Fatal("antes de instalar, checkUnix não deveria achar a fonte")
	}

	var out strings.Builder
	if err := installUnix(&out, linuxFontsDir()); err != nil {
		t.Fatal(err)
	}
	if !sameFile(filepath.Join(linuxFontsDir(), fontFile), symbolsFont) {
		t.Error("fonte não foi copiada")
	}
	if b, err := os.ReadFile(FontconfigPath()); err != nil || !strings.Contains(string(b), fontFamily) {
		t.Errorf("regra do fontconfig não foi criada: %v", err)
	}
	if fams, _ := logoFonts(); !anyContains(fams, fontFamily) {
		t.Errorf("fontconfig não encontra os logos depois de instalar: %v\n%s", fams, out.String())
	}
	if st := checkUnix(); !st.FontInstalled || !st.Ready() {
		t.Errorf("checkUnix deveria ver a fonte instalada: %+v", st)
	}

	// segunda vez: não quebra e continua tudo certo
	if err := installUnix(&out, linuxFontsDir()); err != nil {
		t.Fatal(err)
	}
}

// Quem já usa uma Nerd Font não precisa de nada: o mtsh não instala nem cria regra.
func TestJaTemNerdFont(t *testing.T) {
	if _, err := exec.LookPath("fc-cache"); err != nil {
		t.Skip("sem fontconfig")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	// "instala" a Cascadia Mono NF, que já tem os logos
	if _, err := writeFont(linuxFontsDir(), cascadiaFile, cascadiaFont); err != nil {
		t.Fatal(err)
	}
	exec.Command("fc-cache", "-f", linuxFontsDir()).Run()

	var out strings.Builder
	if err := installUnix(&out, linuxFontsDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "já tem uma fonte com os logos") {
		t.Errorf("deveria reconhecer a Nerd Font existente:\n%s", out.String())
	}
	if _, err := os.Stat(FontconfigPath()); err == nil {
		t.Error("não deveria criar a regra do fontconfig")
	}
	if _, err := os.Stat(filepath.Join(linuxFontsDir(), fontFile)); err == nil {
		t.Error("não deveria instalar a fonte de símbolos")
	}
}

func TestDicaSSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "192.168.1.5 50000 192.168.1.20 22")
	var out strings.Builder
	terminalTips(&out)
	if !strings.Contains(out.String(), "sessão SSH") {
		t.Errorf("deveria avisar da sessão SSH:\n%s", out.String())
	}
}
