// Package importer lê configurações exportadas por outros programas.
package importer

import (
	"encoding/json"
	"fmt"
	"strings"

	"mtsh/internal/config"
)

// Item é um servidor importado, com a senha e a chave que vieram junto.
type Item struct {
	Host     config.Host
	Password string
	Key      string // conteúdo da chave privada
	KeyPath  string // caminho de uma chave privada em arquivo (MobaXterm), pra ler na importação
}

type electermBookmark struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthType   string `json:"authType"`
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
}

type electermGroup struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	BookmarkIDs      []string `json:"bookmarkIds"`
	BookmarkGroupIDs []string `json:"bookmarkGroupIds"`
}

// Electerm lê o arquivo "all data" exportado pelo electerm.
// Grupos viram pastas; conexões que não são SSH (RDP, VNC…) ficam de fora e
// são contadas em skipped.
func Electerm(data []byte) (items []Item, skipped int, err error) {
	var f struct {
		Bookmarks      []electermBookmark `json:"bookmarks"`
		BookmarkGroups []electermGroup    `json:"bookmarkGroups"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, 0, fmt.Errorf("não parece um export do electerm: %w", err)
	}
	if f.Bookmarks == nil {
		return nil, 0, fmt.Errorf("não parece um export do electerm (sem \"bookmarks\")")
	}

	// pasta de cada bookmark, descendo pelos grupos a partir dos que não são filhos de ninguém
	groups := map[string]electermGroup{}
	child := map[string]bool{}
	for _, g := range f.BookmarkGroups {
		groups[g.ID] = g
		for _, c := range g.BookmarkGroupIDs {
			child[c] = true
		}
	}
	folderOf := map[string]string{}
	var walk func(g electermGroup, path string)
	walk = func(g electermGroup, path string) {
		for _, id := range g.BookmarkIDs {
			if _, ok := folderOf[id]; !ok {
				folderOf[id] = path
			}
		}
		for _, c := range g.BookmarkGroupIDs {
			if cg, ok := groups[c]; ok && cg.ID != g.ID {
				walk(cg, join(path, cg.Title))
			}
		}
	}
	for _, g := range f.BookmarkGroups {
		if child[g.ID] {
			continue
		}
		path := g.Title
		if g.ID == "default" {
			path = "" // o grupo padrão do electerm vira a raiz
		}
		walk(g, clean(path))
	}

	used := map[string]bool{}
	for _, b := range f.Bookmarks {
		if (b.Type != "" && b.Type != "ssh") || b.Host == "" {
			skipped++
			continue
		}
		name := strings.TrimSpace(b.Title)
		if name == "" {
			name = b.Host
		}
		// nome é a chave das senhas, então precisa ser único
		base := name
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		used[name] = true

		it := Item{Host: config.Host{
			Name:   name,
			Folder: folderOf[b.ID],
			Host:   strings.TrimSpace(b.Host),
			User:   strings.TrimSpace(b.Username),
			Port:   b.Port,
			Auth:   config.AuthAuto,
		}}
		switch {
		case b.PrivateKey != "":
			it.Host.Auth, it.Key = config.AuthKey, b.PrivateKey
		case b.Password != "":
			it.Host.Auth, it.Password = config.AuthPassword, b.Password
		}
		items = append(items, it)
	}
	return items, skipped, nil
}

func clean(name string) string { return strings.TrimSpace(strings.ReplaceAll(name, "/", "-")) }

func join(parent, name string) string {
	if parent == "" {
		return clean(name)
	}
	return parent + "/" + clean(name)
}
