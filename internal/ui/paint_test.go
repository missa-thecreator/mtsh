package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestClearsBg(t *testing.T) {
	cases := map[string]bool{
		"":                true,  // \x1b[m
		"0":               true,  // reset
		"0;1;32":          true,  // reset + negrito + cor do texto
		"49":              true,  // fundo padrão
		"39":              false, // só a cor do texto
		"1":               false, // negrito
		"0;48;2;35;43;39": false, // reset e já define outro fundo
		"0;41":            false, // reset + fundo vermelho
		"0;38;2;1;48;9":   true,  // os números depois de 38;2 não são fundo
		"48;5;236":        false,
		"0;100":           false,
		"0;38;5;40;1;4":   true,
	}
	for params, want := range cases {
		if got := clearsBg(params); got != want {
			t.Errorf("clearsBg(%q) = %v, quero %v", params, got, want)
		}
	}
}

func TestPaint(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	seq := bgSeq(cPanel)
	if seq == "" {
		t.Fatal("sem código de fundo no modo truecolor")
	}
	in := sAcc.Render("ab") + " cd\n" + sBtnMain.Render(" ok ")
	out := strings.Split(paint(in, 10, cPanel), "\n")
	for i, l := range out {
		if w := lipgloss.Width(l); w != 10 {
			t.Errorf("linha %d com %d colunas, quero 10", i, w)
		}
		if !strings.HasPrefix(l, seq) {
			t.Errorf("linha %d não começa com o fundo da região", i)
		}
	}
	// depois do trecho colorido ("ab"), o fundo da região volta
	if !strings.Contains(out[0], "\x1b[0m"+seq+" cd") {
		t.Errorf("o fundo não voltou depois do trecho colorido: %q", out[0])
	}
	// o botão mantém o fundo dele (âmbar), definido depois do fundo da região
	if !strings.Contains(out[1], "48;2;240;180;41") && !strings.Contains(out[1], "48;2;240;179;40") {
		t.Errorf("o botão perdeu o fundo próprio: %q", out[1])
	}
}

func TestTerminaisComCorCompleta(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		nome string
		env  func(string) string
		want bool
	}{
		{"Windows Terminal (WSL)", env("WT_SESSION", "abc"), true},
		{"VS Code", env("TERM_PROGRAM", "vscode"), true},
		{"kitty", env("KITTY_WINDOW_ID", "1"), true},
		{"terminal desconhecido", env("TERM", "xterm-256color"), false},
		{"NO_COLOR manda", env("WT_SESSION", "abc", "NO_COLOR", "1"), false},
	}
	for _, c := range cases {
		if got := wantTrueColor(c.env); got != c.want {
			t.Errorf("%s: %v, quero %v", c.nome, got, c.want)
		}
	}
}

// Em 256 cores, fundo, painel e seleção precisam continuar diferentes.
func TestTonsDiferentesEm256Cores(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)
	seen := map[string]string{}
	for nome, c := range map[string]lipgloss.TerminalColor{"fundo": cBg, "painel": cPanel, "seleção": cSel} {
		seq := bgSeq(c)
		if outro, ok := seen[seq]; ok {
			t.Errorf("%s e %s viram a mesma cor em 256 cores (%q)", nome, outro, seq)
		}
		seen[seq] = nome
	}
}
