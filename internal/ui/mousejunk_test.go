package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runes(s string, alt bool) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: alt}
}

// As sequências são as que o Bubble Tea v1.3.4 entrega quando "\x1b[<32;61;43M"
// chega partido em dois blocos de leitura.
func TestMouseJunk(t *testing.T) {
	for _, keys := range [][]tea.KeyMsg{
		{runes("[", true), runes("<", false), runes("32;61;43M", false)}, // "\x1b[<" | "32;61;43M"
		{runes("[", true), runes("<32;", false), runes("61;43M", false)}, // "\x1b[<32;" | "61;43M"
		{runes("[", true), runes("<32;61;4", false), runes("3M", false)}, // "\x1b[<32;61;4" | "3M"
		{runes("[<32;61;43m", false)},                                   // "\x1b" | "[<32;61;43m" (o esc passa)
	} {
		m := &Model{}
		for _, k := range keys {
			if !m.mouseJunk(k) {
				t.Errorf("%q passou pra sessão (sequência %v)", k.String(), keys)
			}
		}
	}
	// digitação normal continua passando, inclusive "<", "3M" e números
	m := &Model{}
	for _, s := range []string{"<", "3M", "M", "1;", "echo 1;2", "a"} {
		if m.mouseJunk(runes(s, false)) {
			t.Errorf("%q foi descartado", s)
		}
	}
	if m.mouseJunk(runes("b", true)) || m.mouseJunk(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("61;43M"), Paste: true}) {
		t.Error("alt+b ou colagem foi descartado")
	}
	// depois de um pedaço completo, o próximo "<" digitado passa
	m.mouseJunk(runes("[", true))
	m.mouseJunk(runes("<32;61;43M", false))
	if m.mouseJunk(runes("<", false)) {
		t.Error(`"<" depois do fim do pedaço foi descartado`)
	}
}
