package dbconn

import (
	"strings"
	"unicode"
)

// Stmt é um comando dentro do texto do editor; Start e End são posições em runas.
type Stmt struct {
	Start, End int
	Text       string
}

// Split separa os comandos por ";", sem cortar dentro de textos ('…', "…",
// `…`), comentários (-- e /* */) nem blocos $$…$$ do Postgres.
func Split(text string) []Stmt {
	r := []rune(text)
	var out []Stmt
	start := 0
	add := func(end int) {
		if t := strings.TrimSpace(string(r[start:end])); t != "" && strings.TrimSpace(stripComments(t)) != "" {
			out = append(out, Stmt{start, end, t})
		}
	}
	for i := 0; i < len(r); i++ {
		switch c := r[i]; {
		case c == '\'' || c == '"' || c == '`':
			for i++; i < len(r); i++ {
				if r[i] == '\\' && c != '`' {
					i++ // \' dentro de texto (MySQL)
				} else if r[i] == c {
					if i+1 < len(r) && r[i+1] == c {
						i++ // '' é aspas escapada
						continue
					}
					break
				}
			}
		case c == '-' && i+1 < len(r) && r[i+1] == '-', c == '#':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			for i += 2; i+1 < len(r) && !(r[i] == '*' && r[i+1] == '/'); i++ {
			}
			i++
		case c == '$':
			// $$ ou $tag$: vai até a mesma marca
			j := i + 1
			for j < len(r) && (r[j] == '_' || r[j] >= 'a' && r[j] <= 'z' || r[j] >= 'A' && r[j] <= 'Z' || r[j] >= '0' && r[j] <= '9') {
				j++
			}
			if j < len(r) && r[j] == '$' {
				tag := string(r[i : j+1])
				if k := strings.Index(string(r[j+1:]), tag); k >= 0 {
					i = j + 1 + len([]rune(string(r[j+1:])[:k])) + len([]rune(tag)) - 1
				} else {
					i = len(r)
				}
			}
		case c == ';':
			add(i)
			start = i + 1
		}
	}
	add(len(r))
	return out
}

// At devolve o comando onde está o cursor (pos em runas): o último que começa
// antes dele. Assim, o cursor logo depois do ";" ou numa linha em branco
// depois do comando ainda pega esse comando.
func At(text string, pos int) (Stmt, bool) {
	stmts := Split(text)
	if len(stmts) == 0 {
		return Stmt{}, false
	}
	r := []rune(text)
	best := stmts[0]
	for _, s := range stmts {
		start := s.Start
		for start < len(r) && unicode.IsSpace(r[start]) {
			start++
		}
		if start <= pos {
			best = s
		}
	}
	return best, true
}

// stripComments tira os comentários de -- e /* */ (pra olhar a primeira palavra).
func stripComments(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		switch {
		case r[i] == '-' && i+1 < len(r) && r[i+1] == '-', r[i] == '#':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		case r[i] == '/' && i+1 < len(r) && r[i+1] == '*':
			for i += 2; i+1 < len(r) && !(r[i] == '*' && r[i+1] == '/'); i++ {
			}
			i++
		default:
			b.WriteRune(r[i])
		}
	}
	return b.String()
}
