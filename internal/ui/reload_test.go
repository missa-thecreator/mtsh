package ui

import (
	"path/filepath"
	"testing"
	"time"

	"mtsh/internal/config"
	"mtsh/internal/vault"
)

// Outra janela salvou um servidor; esta, ao salvar, recarrega em vez de apagá-lo.
func TestPersistReloadsWhenOtherWindowSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := (&config.File{Folders: []string{"A"}}).Save(path); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(path)
	m := New(path, cfg, &vault.Vault{}, "dev")

	time.Sleep(10 * time.Millisecond)
	other, _ := config.Load(path)
	other.Hosts = append(other.Hosts, config.Host{Name: "novo", Host: "1", Folder: "B"})
	if err := other.Save(path); err != nil {
		t.Fatal(err)
	}

	m.cfg.SideWidth = 40
	if m.persist() {
		t.Fatal("salvou por cima da outra janela")
	}
	if len(m.cfg.Hosts) != 1 || !m.open["B"] {
		t.Fatalf("não recarregou: hosts=%v open=%v", m.cfg.Hosts, m.open)
	}
	if !m.persist() {
		t.Fatal("depois de recarregar, não salvou")
	}
}
