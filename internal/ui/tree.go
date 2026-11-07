package ui

import "mtsh/internal/config"

type rowKind int

const (
	rowAll rowKind = iota
	rowFolder
	rowHost
)

// treeRow é uma linha da árvore da barra lateral.
type treeRow struct {
	kind  rowKind
	path  string      // pasta (rowFolder)
	host  config.Host // rowHost
	depth int
}

// treeRows monta a árvore: "Todos", depois pastas (com subpastas e hosts
// quando abertas) e por fim os hosts sem pasta.
func (m *Model) treeRows() []treeRow {
	rows := []treeRow{{kind: rowAll}}
	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		for _, f := range m.cfg.Children(path) {
			rows = append(rows, treeRow{kind: rowFolder, path: f, depth: depth})
			if m.open[f] {
				walk(f, depth+1)
				for _, h := range m.cfg.HostsIn(f) {
					rows = append(rows, treeRow{kind: rowHost, host: h, depth: depth + 1})
				}
			}
		}
	}
	walk("", 0)
	for _, h := range m.cfg.HostsIn("") {
		rows = append(rows, treeRow{kind: rowHost, host: h})
	}
	return rows
}
