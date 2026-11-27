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

// Renomear "Prod" leva junto subpastas, servidores e estilos dele, e não toca
// em "Produção", que só começa com as mesmas letras.
func TestRenameFolderOnlyMovesThatFolder(t *testing.T) {
	f := &File{
		Folders: []string{"Prod", "Prod/Web", "Produção"},
		FolderStyles: map[string]FolderStyle{
			"Prod/Web": {Color: "azul"}, "Produção": {Color: "verde"},
		},
		Hosts: []Host{
			{Name: "a", Folder: "Prod"}, {Name: "b", Folder: "Prod/Web"}, {Name: "c", Folder: "Produção"},
		},
	}
	f.RenameFolder("Prod", "Clientes/Prod2")

	if want := []string{"Clientes", "Clientes/Prod2", "Clientes/Prod2/Web", "Produção"}; !slices.Equal(f.Folders, want) {
		t.Fatalf("pastas: %q, esperava %q", f.Folders, want)
	}
	folderOf := map[string]string{}
	for _, h := range f.Hosts {
		folderOf[h.Name] = h.Folder
	}
	if folderOf["a"] != "Clientes/Prod2" || folderOf["b"] != "Clientes/Prod2/Web" || folderOf["c"] != "Produção" {
		t.Fatalf("servidores: %v", folderOf)
	}
	if f.Style("Clientes/Prod2/Web").Color != "azul" || f.Style("Produção").Color != "verde" || len(f.FolderStyles) != 2 {
		t.Fatalf("estilos: %v", f.FolderStyles)
	}

	// apagar "Clientes/Prod2" leva as subpastas e os estilos delas, e só elas
	f.DeleteFolder("Clientes/Prod2")
	if want := []string{"Clientes", "Produção"}; !slices.Equal(f.Folders, want) {
		t.Fatalf("depois de apagar: %q", f.Folders)
	}
	if len(f.FolderStyles) != 1 {
		t.Fatalf("estilos depois de apagar: %v", f.FolderStyles)
	}
}

// Normalize cria as pastas-pai que faltam (de servidores e de subpastas),
// tira "/" das pontas e descarta estilo de pasta que não existe mais.
func TestNormalize(t *testing.T) {
	f := &File{
		Folders:      []string{"x/y/"},
		FolderStyles: map[string]FolderStyle{"sumiu": {Icon: "📁"}},
		Hosts:        []Host{{Name: "b", Folder: "/a/b/"}, {Name: "a", Folder: "a/b"}},
	}
	f.Normalize()
	if want := []string{"a", "a/b", "x", "x/y"}; !slices.Equal(f.Folders, want) {
		t.Fatalf("pastas: %q", f.Folders)
	}
	if f.Hosts[0].Name != "a" || f.Hosts[1].Folder != "a/b" || len(f.FolderStyles) != 0 {
		t.Fatalf("hosts=%v estilos=%v", f.Hosts, f.FolderStyles)
	}
}
