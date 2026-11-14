package session

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.ReplaceAllString(l, ""), " ")
	}
	return out
}

// roda um comando num PTY de 20x10 e espera ele terminar
func run(t *testing.T, script string) *Session {
	t.Helper()
	s, err := Start("teste", exec.Command("sh", "-c", script), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-s.Updates():
			if !ok {
				return s
			}
		case <-deadline:
			t.Fatal("o comando não terminou")
		}
	}
}

func TestRolagemMostraOQueSaiuDaTela(t *testing.T) {
	s := run(t, "seq 1 100")
	if l := plain(s.Lines(false)); l[len(l)-2] != "100" {
		t.Fatalf("fim da tela deveria mostrar 100: %q", l)
	}
	if off, hist := s.Scroll(); off != 0 || hist < 90 {
		t.Fatalf("Scroll() = %d, %d", off, hist)
	}

	s.ScrollBy(1000) // sobe tudo: começo da saída
	if l := plain(s.Lines(false)); l[0] != "1" || l[9] != "10" {
		t.Errorf("topo do histórico deveria ser 1..10: %q", l)
	}
	s.ScrollBy(-5)
	if l := plain(s.Lines(false)); l[0] != "6" {
		t.Errorf("descendo 5 deveria começar no 6: %q", l)
	}
	s.ScrollToBottom()
	if off, _ := s.Scroll(); off != 0 {
		t.Errorf("deveria voltar pro fim, offset=%d", off)
	}
}

func TestRoladoFicaParadoComSaidaNova(t *testing.T) {
	s, err := Start("teste", exec.Command("sh", "-c", "seq 1 50; sleep 0.5; seq 51 80; sleep 0.3"), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	s.ScrollBy(5)
	antes := plain(s.Lines(false))[0]
	for range s.Updates() {
	}
	if depois := plain(s.Lines(false))[0]; depois != antes {
		t.Errorf("com a saída nova, o trecho lido mudou de %q pra %q", antes, depois)
	}
}

func TestTelaCheiaSemHistorico(t *testing.T) {
	s := run(t, `printf '\033[?1049h'; seq 1 40; sleep 0.2`)
	if !s.AltScreen() {
		t.Skip("o programa já saiu da tela alternativa")
	}
	if _, hist := s.Scroll(); hist != 0 {
		t.Errorf("tela alternativa não deveria gerar histórico: %d linhas", hist)
	}
}
