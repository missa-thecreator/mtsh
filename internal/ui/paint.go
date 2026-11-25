package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Fundos por região (abas, lateral, área principal, rodapé).
//
// O lipgloss zera o fundo a cada trecho colorido ("\x1b[0m"), então pintar
// um bloco inteiro com Background() só pinta até o primeiro trecho colorido.
// paint resolve isso por fora: depois de cada código que zera o fundo, devolve
// o fundo da região. Quem tem fundo próprio (botões, linha selecionada) não é
// afetado, porque define o fundo depois disso.

var sgrRe = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// bgSeq é o código que liga o fundo c no perfil de cor do terminal atual
// ("" se o terminal não tem cor; aí paint não faz nada).
func bgSeq(c lipgloss.TerminalColor) string {
	r := lipgloss.NewStyle().Background(c).Render("X")
	if i := strings.Index(r, "X"); i > 0 {
		return r[:i]
	}
	return ""
}

// clearsBg diz se o código SGR termina com o fundo zerado (reset ou 49)
// sem definir outro fundo depois.
func clearsBg(params string) bool {
	if params == "" {
		return true // "\x1b[m" é reset
	}
	ps := strings.Split(strings.ReplaceAll(params, ":", ";"), ";")
	cleared, set := false, false
	for i := 0; i < len(ps); i++ {
		switch p := ps[i]; {
		case p == "0" || p == "":
			cleared, set = true, false
		case p == "49":
			cleared, set = true, false
		case p == "48":
			set = true
			i += skipColorArgs(ps, i)
		case p == "38" || p == "58":
			i += skipColorArgs(ps, i)
		case len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7',
			len(p) == 3 && strings.HasPrefix(p, "10") && p[2] >= '0' && p[2] <= '7':
			set = true
		}
	}
	return cleared && !set
}

// skipColorArgs pula os argumentos de 38/48 (";5;n" ou ";2;r;g;b").
func skipColorArgs(ps []string, i int) int {
	if i+1 < len(ps) {
		switch ps[i+1] {
		case "5":
			return 2
		case "2":
			return 4
		}
	}
	return 0
}

// paint pinta o bloco com o fundo c: cada linha fica com exatamente width
// colunas e o fundo volta depois de todo trecho que o zera.
func paint(block string, width int, c lipgloss.TerminalColor) string {
	seq := bgSeq(c)
	if seq == "" {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		l = sgrRe.ReplaceAllStringFunc(l, func(s string) string {
			if clearsBg(s[2 : len(s)-1]) {
				return s + seq
			}
			return s
		})
		if w := lipgloss.Width(l); w < width {
			l += strings.Repeat(" ", width-w)
		}
		lines[i] = seq + l + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
