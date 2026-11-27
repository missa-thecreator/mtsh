package ui

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mtsh/internal/config"
	"mtsh/internal/session"
	"mtsh/internal/vault"
)

// Arrastando com o mouse parado acima da aba, cada tique sobe (1 linha no
// começo, mais depois de um tempo); soltar o botão para a rolagem.
func TestDragRollKeepsScrolling(t *testing.T) {
	s, err := session.Start("x", exec.Command("sh", "-c", "seq 1 200; sleep 5"), 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, h := s.Scroll(); h < 100; _, h = s.Scroll() {
		time.Sleep(10 * time.Millisecond)
	}
	m := New(filepath.Join(t.TempDir(), "h.yaml"), &config.File{}, &vault.Vault{}, "dev")
	m.width, m.height, m.sessions, m.tab = 120, 30, []*session.Session{s}, 1
	m.dragging, m.dragX, m.dragY = true, 5, -1
	for i := 1; i <= 3; i++ {
		if m.dragRoll(s) == nil {
			t.Fatalf("tique %d não agendou o próximo", i)
		}
		if off, _ := s.Scroll(); off != i {
			t.Fatalf("tique %d: rolou %d linhas", i, off)
		}
	}
	m.rollN = 30 // meio segundo segurando: 3 linhas por tique
	m.dragRoll(s)
	if off, _ := s.Scroll(); off != 6 {
		t.Fatalf("acelerado: rolou %d linhas no total, esperava 6", off)
	}
	m.dragging = false
	if m.dragRoll(s) != nil {
		t.Fatal("continuou rolando depois de soltar")
	}
}
