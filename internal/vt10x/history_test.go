package vt10x

import (
	"fmt"
	"strings"
	"testing"
)

func lineText(g []Glyph) string {
	var b strings.Builder
	for _, c := range g {
		if c.Char == 0 {
			b.WriteRune(' ')
		} else {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func newTerm(cols, rows int) *terminal {
	return New(WithSize(cols, rows)).(*terminal)
}

func TestHistoricoGuardaOQueSaiPeloTopo(t *testing.T) {
	term := newTerm(20, 5)
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(term, "linha %d\r\n", i)
	}
	// 5 linhas na tela: linha 9..12 + linha vazia do cursor; o resto no histórico
	if n := term.HistoryLen(); n != 8 {
		t.Fatalf("histórico com %d linhas, quero 8", n)
	}
	if got := lineText(term.HistoryLine(0)); got != "linha 1" {
		t.Errorf("primeira linha do histórico = %q", got)
	}
	if got := lineText(term.HistoryLine(7)); got != "linha 8" {
		t.Errorf("última linha do histórico = %q", got)
	}
}

func TestTelaAlternativaNaoEntraNoHistorico(t *testing.T) {
	term := newTerm(20, 5)
	term.Write([]byte("\x1b[?1049h")) // entra na tela alternativa (vim, htop, less…)
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(term, "tela cheia %d\r\n", i)
	}
	if n := term.HistoryLen(); n != 0 {
		t.Errorf("tela alternativa não pode ir pro histórico, mas guardou %d linhas", n)
	}
	term.Write([]byte("\x1b[?1049l")) // volta pra tela principal
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(term, "shell %d\r\n", i)
	}
	if n := term.HistoryLen(); n == 0 {
		t.Error("de volta à tela principal, o histórico deveria voltar a crescer")
	}
}

func TestClearApagaHistorico(t *testing.T) {
	term := newTerm(20, 5)
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(term, "linha %d\r\n", i)
	}
	term.Write([]byte("\x1b[H\x1b[2J\x1b[3J")) // o que o comando clear manda
	if n := term.HistoryLen(); n != 0 {
		t.Errorf("clear deveria apagar o histórico, sobrou %d", n)
	}
}

func TestHistoricoTemLimite(t *testing.T) {
	term := newTerm(10, 3)
	for i := 0; i < HistoryMax+500; i++ {
		term.Write([]byte("x\r\n"))
	}
	if n := term.HistoryLen(); n != HistoryMax {
		t.Errorf("histórico com %d linhas, limite é %d", n, HistoryMax)
	}
}
