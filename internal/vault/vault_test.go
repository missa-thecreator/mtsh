package vault

import (
	"path/filepath"
	"testing"
)

// Duas janelas abertas: a senha salva numa não some quando a outra salva.
func TestTwoWindowsKeepEachOthersPasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "passwords.json")
	w1, _ := Open(path)
	w2, _ := Open(path)
	if err := w1.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := w2.Set("b", "2"); err != nil {
		t.Fatal(err)
	}
	if err := w2.Rename("b", "c"); err != nil {
		t.Fatal(err)
	}
	got, _ := Open(path)
	if p, _ := got.Get("a"); p != "1" || !got.Has("c") || got.Has("b") {
		t.Fatalf("senhas no arquivo: %v", got.entries)
	}
}
