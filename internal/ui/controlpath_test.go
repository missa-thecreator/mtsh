package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// A pasta dos sockets só é aceita se for uma pasta de verdade, deste usuário,
// fechada pros outros.
func TestPrivateDir(t *testing.T) {
	base := t.TempDir()
	ok := filepath.Join(base, "ok")
	os.Mkdir(ok, 0o700)
	if err := privateDir(ok); err != nil {
		t.Fatalf("pasta 700 recusada: %v", err)
	}
	open := filepath.Join(base, "aberta")
	os.Mkdir(open, 0o700)
	os.Chmod(open, 0o777)
	if privateDir(open) == nil {
		t.Fatal("aceitou pasta que outros podem escrever")
	}
	link := filepath.Join(base, "link")
	os.Symlink(ok, link)
	if privateDir(link) == nil {
		t.Fatal("aceitou um link no lugar da pasta")
	}
}
