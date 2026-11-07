package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Host é um servidor cadastrado.
type Host struct {
	Name   string   `yaml:"name"`
	Folder string   `yaml:"folder,omitempty"` // caminho tipo "Trabalho/Produção"; vazio = raiz
	Host   string   `yaml:"host"`
	User   string   `yaml:"user,omitempty"`
	Port   int      `yaml:"port,omitempty"`
	Auth   string   `yaml:"auth,omitempty"` // password | key | auto
	Key    string   `yaml:"key,omitempty"`  // caminho da chave privada
	Tags   []string `yaml:"tags,omitempty"`

	// detectados ao conectar (ver internal/osinfo)
	OS     string `yaml:"os,omitempty"`      // ubuntu, debian, alpine…
	OSLike string `yaml:"os_like,omitempty"` // família, ex: debian
	OSName string `yaml:"os_name,omitempty"` // ex: "Ubuntu 24.04"
}

const (
	AuthPassword = "password"
	AuthKey      = "key"
	AuthAuto     = "auto"
)

// AuthType devolve o tipo de acesso, deduzindo dos campos em configs antigas.
func (h Host) AuthType() string {
	switch {
	case h.Auth != "":
		return h.Auth
	case h.Key != "":
		return AuthKey
	}
	return AuthAuto
}

// KeysDir é onde ficam as chaves coladas pelo mtsh.
func KeysDir(cfgPath string) string { return filepath.Join(filepath.Dir(cfgPath), "keys") }

// PasswordsPath é o arquivo das senhas salvas (permissão 600).
func PasswordsPath(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "passwords.json")
}

// Target devolve "user@host" (ou só "host").
func (h Host) Target() string {
	if h.User != "" {
		return h.User + "@" + h.Host
	}
	return h.Host
}

// SSHArgs monta os argumentos para o binário ssh.
func (h Host) SSHArgs() []string {
	var args []string
	if h.Port != 0 && h.Port != 22 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	if h.AuthType() == AuthKey && h.Key != "" {
		args = append(args, "-i", ExpandHome(h.Key), "-o", "IdentitiesOnly=yes")
	}
	if h.AuthType() == AuthPassword {
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive")
	}
	return append(args, h.Target())
}

// InFolder diz se o host está na pasta p ou em alguma subpasta dela.
// p vazio significa "todos".
func (h Host) InFolder(p string) bool {
	return p == "" || h.Folder == p || strings.HasPrefix(h.Folder, p+"/")
}

// FolderStyle é a aparência de uma pasta: ícone (emoji) e cor (nome da paleta).
type FolderStyle struct {
	Icon  string `yaml:"icon,omitempty"`
	Color string `yaml:"color,omitempty"`
}

type File struct {
	// SimpleIcons troca o logo das distros (precisa de Nerd Font no terminal)
	// por uma bolinha na cor da distro
	SimpleIcons bool `yaml:"simple_icons,omitempty"`

	Folders      []string               `yaml:"folders,omitempty"`
	FolderStyles map[string]FolderStyle `yaml:"folder_styles,omitempty"` // chave = caminho da pasta
	Hosts        []Host                 `yaml:"hosts"`
}

// Style devolve o ícone e a cor da pasta p.
func (f *File) Style(p string) FolderStyle { return f.FolderStyles[p] }

// SetStyle define o ícone e a cor da pasta p (vazio remove).
func (f *File) SetStyle(p string, st FolderStyle) {
	if st == (FolderStyle{}) {
		delete(f.FolderStyles, p)
		return
	}
	if f.FolderStyles == nil {
		f.FolderStyles = map[string]FolderStyle{}
	}
	f.FolderStyles[p] = st
}

// under diz se x é a pasta p ou está dentro dela.
func under(x, p string) bool { return x == p || strings.HasPrefix(x, p+"/") }

// moved troca o prefixo old por new em x (x precisa estar em old).
func moved(x, old, new string) string { return new + strings.TrimPrefix(x, old) }

// RenameFolder muda o caminho de uma pasta (renomear ou mover), levando junto
// subpastas, servidores e estilos.
func (f *File) RenameFolder(old, new string) {
	for i, x := range f.Folders {
		if under(x, old) {
			f.Folders[i] = moved(x, old, new)
		}
	}
	for i, h := range f.Hosts {
		if under(h.Folder, old) {
			f.Hosts[i].Folder = moved(h.Folder, old, new)
		}
	}
	styles := map[string]FolderStyle{}
	for k, v := range f.FolderStyles {
		if under(k, old) {
			k = moved(k, old, new)
		}
		styles[k] = v
	}
	f.FolderStyles = styles
	f.Normalize()
}

// DeleteFolder apaga a pasta p e as subpastas (não mexe nos servidores).
func (f *File) DeleteFolder(p string) {
	var keep []string
	for _, x := range f.Folders {
		if !under(x, p) {
			keep = append(keep, x)
		}
	}
	f.Folders = keep
	for k := range f.FolderStyles {
		if under(k, p) {
			delete(f.FolderStyles, k)
		}
	}
}

// DefaultPath: $MTSH_CONFIG ou ~/.config/mtsh/hosts.yaml
func DefaultPath() string {
	if p := os.Getenv("MTSH_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "mtsh", "hosts.yaml")
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	f.Normalize()
	return &f, nil
}

func (f *File) Save(path string) error {
	f.Normalize()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Normalize garante que toda pasta usada por um host (e seus pais) exista
// na lista de pastas, e ordena tudo.
func (f *File) Normalize() {
	set := map[string]bool{}
	add := func(p string) {
		p = strings.Trim(p, "/")
		for p != "" {
			set[p] = true
			p = Parent(p)
		}
	}
	for _, p := range f.Folders {
		add(p)
	}
	for i := range f.Hosts {
		f.Hosts[i].Folder = strings.Trim(f.Hosts[i].Folder, "/")
		add(f.Hosts[i].Folder)
	}
	f.Folders = f.Folders[:0]
	for p := range set {
		f.Folders = append(f.Folders, p)
	}
	sort.Strings(f.Folders)
	for k := range f.FolderStyles {
		if !set[k] {
			delete(f.FolderStyles, k) // estilo de pasta que não existe mais
		}
	}
	sort.SliceStable(f.Hosts, func(i, j int) bool {
		a, b := f.Hosts[i], f.Hosts[j]
		if a.Folder != b.Folder {
			return a.Folder < b.Folder
		}
		return a.Name < b.Name
	})
}

// Children devolve as subpastas diretas de p.
func (f *File) Children(p string) []string {
	var out []string
	for _, x := range f.Folders {
		if Parent(x) == p {
			out = append(out, x)
		}
	}
	return out
}

// HostsIn devolve os hosts que estão diretamente na pasta p.
func (f *File) HostsIn(p string) []Host {
	var out []Host
	for _, h := range f.Hosts {
		if h.Folder == p {
			out = append(out, h)
		}
	}
	return out
}

// Count devolve quantos hosts estão em p (incluindo subpastas).
func (f *File) Count(p string) int {
	n := 0
	for _, h := range f.Hosts {
		if h.InFolder(p) {
			n++
		}
	}
	return n
}

func Parent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func Base(p string) string {
	return p[strings.LastIndex(p, "/")+1:]
}

func ExpandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
