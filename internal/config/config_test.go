package config

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// O alvo vai depois de "--": um host ou usuário começando com "-" não pode
// virar opção do ssh (ex: -oProxyCommand=…, que executa comandos).
func TestSSHArgsTargetIsNeverAnOption(t *testing.T) {
	h := Host{Host: "-oProxyCommand=touch /tmp/x", User: "root", Port: 2222}
	args := h.SSHArgs()
	i := slices.Index(args, "--")
	if i < 0 || i != len(args)-2 || args[i+1] != "root@-oProxyCommand=touch /tmp/x" {
		t.Fatalf("alvo fora do lugar: %q", args)
	}
}

// Duas janelas com a mesma config: a segunda a salvar não pode apagar o que
// a primeira gravou; recebe ErrChanged e, relendo, salva normalmente.
func TestSaveRefusesToOverwriteOtherWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := (&File{Hosts: []Host{{Name: "a", Host: "1"}}}).Save(path); err != nil {
		t.Fatal(err)
	}
	w1, _ := Load(path)
	w2, _ := Load(path)
	time.Sleep(10 * time.Millisecond) // garante data de modificação diferente
	w1.Hosts = append(w1.Hosts, Host{Name: "novo", Host: "2"})
	if err := w1.Save(path); err != nil {
		t.Fatal(err)
	}
	w2.SideWidth = 40
	if err := w2.Save(path); !errors.Is(err, ErrChanged) {
		t.Fatalf("segunda janela sobrescreveu (err=%v)", err)
	}
	if got, _ := Load(path); len(got.Hosts) != 2 {
		t.Fatalf("servidor novo sumiu: %v", got.Hosts)
	}
	w2, _ = Load(path)
	w2.SideWidth = 40
	if err := w2.Save(path); err != nil {
		t.Fatalf("depois de reler, não salvou: %v", err)
	}
	if err := w2.Save(path); err != nil {
		t.Fatalf("salvar de novo na mesma janela falhou: %v", err)
	}
}
