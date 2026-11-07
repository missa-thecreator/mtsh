package fonts

import (
	"errors"
	"os"
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
