package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mtsh/internal/vt10x"
)

func selectText(s *Session, x0, y0, x1, y1 int) string {
	s.StartSelection(s.PointAt(x0, y0))
	s.ExtendSelection(s.PointAt(x1, y1))
	return s.SelectionText()
}

func TestSelecaoNumaLinha(t *testing.T) {
	s := run(t, "printf 'abc def\\nlinha2\\n'")
	if got := selectText(s, 0, 0, 2, 0); got != "abc" {
		t.Fatalf("got %q", got)
	}
	// espaços no fim não entram, mesmo marcando até a borda
	if got := selectText(s, 4, 0, 19, 0); got != "def" {
		t.Fatalf("got %q", got)
	}
}

func TestSelecaoVariasLinhasNosDoisSentidos(t *testing.T) {
	s := run(t, "printf 'abc def\\nlinha2\\n'")
	if got := selectText(s, 4, 0, 2, 1); got != "def\nlin" {
		t.Fatalf("pra baixo: %q", got)
	}
	if got := selectText(s, 2, 1, 4, 0); got != "def\nlin" {
		t.Fatalf("pra cima: %q", got)
	}
}

func TestSelecaoJuntaLinhaQuebrada(t *testing.T) {
	long := strings.Repeat("x", 25) // a tela tem 20 colunas: quebra em duas
	s := run(t, "printf '"+long+"\\nfim\\n'")
	if got := selectText(s, 0, 0, 19, 2); got != long+"\nfim" {
		t.Fatalf("got %q", got)
	}
}

func TestSelecaoNoHistoricoEDestaque(t *testing.T) {
	s := run(t, "seq 1 30")
	s.ScrollBy(1000) // começo da saída
	if got := selectText(s, 0, 0, 5, 1); got != "1\n2" {
		t.Fatalf("got %q", got)
	}
	// as células marcadas aparecem invertidas
	if l := s.Lines(false); !strings.Contains(l[0], ";7") {
		t.Fatalf("seleção sem destaque: %q", l[0])
	}
	// clique parado não é seleção
	s.StartSelection(s.PointAt(3, 3))
	if s.HasSelection() || s.SelectionText() != "" {
		t.Fatal("clique sem arrastar virou seleção")
	}
}

func TestSelecaoNaoEscorregaComSaidaNova(t *testing.T) {
	s, err := Start("teste", exec.Command("sh", "-c", "echo marca; sleep 0.3; seq 1 30"), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, func() bool { return strings.HasPrefix(plain(s.Lines(false))[0], "marca") })
	s.StartSelection(s.PointAt(0, 0))
	s.ExtendSelection(s.PointAt(4, 0))
	for range s.Updates() { // espera o seq terminar e empurrar "marca" pro histórico
	}
	if got := s.SelectionText(); got != "marca" {
		t.Fatalf("seleção mudou de lugar: %q", got)
	}
}

func TestColarComBracketedPaste(t *testing.T) {
	out := filepath.Join(t.TempDir(), "colado")
	// o programa liga o bracketed paste e grava os bytes crus que receber
	script := `printf '\033[?2004h'; stty raw -echo; head -c 15 > "$OUT"`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "OUT="+out)
	s, err := Start("teste", cmd, 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, func() bool { return s.term.Mode()&vt10x.ModeBracketedPaste != 0 })
	time.Sleep(300 * time.Millisecond) // tempo do stty raw

	s.Paste("a\nb")
	for range s.Updates() {
	}
	got, _ := os.ReadFile(out)
	if want := "\x1b[200~a\rb\x1b[201~"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("tempo esgotado")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
