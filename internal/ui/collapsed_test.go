package ui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/config"
	"mtsh/internal/vault"
)

func TestCollapsedFoldersSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	cfg := &config.File{Folders: []string{"A", "A/B", "C"}, Collapsed: []string{"C"}}
	m := New(path, cfg, &vault.Vault{}, "dev")
	if !m.open["A"] || !m.open["A/B"] || m.open["C"] {
		t.Fatalf("estado inicial errado: %v", m.open)
	}

	// fecha A pela árvore: linhas são Importar, Todos, Bancos, Clusters, A, A/B, C
	m.focus, m.sideSel = focusSide, 4
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Collapsed; len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Fatalf("collapsed salvo = %v, quero [A C]", got)
	}
	if m2 := New(path, saved, &vault.Vault{}, "dev"); m2.open["A"] || m2.open["C"] || !m2.open["A/B"] {
		t.Fatalf("depois de reabrir: %v", m2.open)
	}
}
