package ui

import (
	"reflect"
	"regexp"
	"testing"

	"mtsh/internal/config"
)

func TestParseTags(t *testing.T) {
	got := parseTags(" Prod, banco de dados,,prod , K8S ")
	want := []string{"prod", "banco-de-dados", "k8s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTags = %q, quero %q", got, want)
	}
	if parseTags("  ,  ") != nil {
		t.Error("campo vazio deveria dar nenhuma tag")
	}
}

func TestSugestaoECompletar(t *testing.T) {
	existing := []string{"banco", "producao", "prod"}
	cases := []struct{ input, want string }{
		{"pr", "producao"},        // primeira em ordem que começa com "pr"
		{"prod", "producao"},      // "prod" já é exata; sugere a outra
		{"banco, pr", "producao"}, // olha só o trecho depois da vírgula
		{"producao, prod", ""},    // nada além do que já foi digitado
		{"", ""},                  // nada digitado
		{"x", ""},                 // nada começa com x
		{"producao, p", "prod"},   // não sugere a que já foi digitada
	}
	for _, c := range cases {
		if got := suggestTag(c.input, existing); got != c.want {
			t.Errorf("suggestTag(%q) = %q, quero %q", c.input, got, c.want)
		}
	}
	if got := completeTag("banco, pr", "producao"); got != "banco, producao, " {
		t.Errorf("completeTag = %q", got)
	}
	if got := completeTag("pr", "producao"); got != "producao, " {
		t.Errorf("completeTag sem vírgula = %q", got)
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestLinhaDeTagsDoCard(t *testing.T) {
	h := config.Host{OS: "ubuntu", OSName: "Ubuntu 24.04", Tags: []string{"prod", "banco"}}
	line, spans := cardTagsLine(h, 40)
	plain := ansiRe.ReplaceAllString(line, "")
	if plain != "Ubuntu 24.04 · prod  banco" {
		t.Fatalf("linha = %q", plain)
	}
	// cada span tem que apontar exatamente pra tag no texto
	for _, s := range spans {
		if got := []rune(plain)[s.start:s.end]; string(got) != s.tag {
			t.Errorf("span de %q aponta pra %q", s.tag, string(got))
		}
	}

	// sem espaço: a tag que não cabe vira "…" e não pode ser clicada
	_, spans = cardTagsLine(h, 20)
	if len(spans) != 1 || spans[0].tag != "prod" {
		t.Errorf("com 20 colunas deveria caber só prod: %+v", spans)
	}

	// sem sistema e sem tags
	if line, spans := cardTagsLine(config.Host{}, 40); spans != nil || ansiRe.ReplaceAllString(line, "") != "sistema aparece ao conectar" {
		t.Errorf("card vazio: %q %+v", line, spans)
	}
}

func TestFiltroPorTag(t *testing.T) {
	h := config.Host{Tags: []string{"producao", "web"}}
	if !matchTag(h, "pro", false) || matchTag(h, "pro", true) {
		t.Error("enquanto digita vale o começo; exata só com o nome inteiro")
	}
	if !matchTag(h, "web", true) {
		t.Error("tag exata deveria casar")
	}
}
