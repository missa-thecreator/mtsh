package ui

import (
	"os"
	"path/filepath"
	"testing"

	"mtsh/internal/config"
	"mtsh/internal/importer"
	"mtsh/internal/vault"
)

// "web/api" e "web-api" virariam o mesmo arquivo de chave; cada um precisa
// ficar com a sua, na importação e no cadastro à mão.
func TestKeyFilesDoNotCollide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	m := New(path, &config.File{}, &vault.Vault{}, "dev")
	_, _, err := m.importItems([]importer.Item{
		{Host: config.Host{Name: "web/api", Host: "1", Auth: config.AuthKey}, Key: "CHAVE-1"},
		{Host: config.Host{Name: "web-api", Host: "2", Auth: config.AuthKey}, Key: "CHAVE-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, h := range m.cfg.Hosts {
		b, _ := os.ReadFile(h.Key)
		keys[h.Name] = string(b)
	}
	if keys["web/api"] != "CHAVE-1\n" || keys["web-api"] != "CHAVE-2\n" {
		t.Fatalf("chaves trocadas ou sobrescritas: %q", keys)
	}

	// cadastro à mão de outro "web-api": não pode cair em arquivo de ninguém
	dest := m.keyFile("web-api", "", nil)
	for _, h := range m.cfg.Hosts {
		if h.Key == dest {
			t.Fatalf("%s já é a chave de %s", dest, h.Name)
		}
	}
	// editando o próprio servidor, reusa o arquivo dele
	var own string
	for _, h := range m.cfg.Hosts {
		if h.Name == "web/api" {
			own = h.Key
		}
	}
	if got := m.keyFile("web/api", "web/api", nil); got != own {
		t.Fatalf("editando, foi pra %s em vez de %s", got, own)
	}
}
