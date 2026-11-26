package importer

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"mtsh/internal/config"
)

// MobaXterm lê as sessões exportadas pelo MobaXterm (.mxtsessions) ou o
// próprio MobaXterm.ini. O formato não é documentado; os campos seguem a
// engenharia reversa em https://gist.github.com/Ruzgfpegk/ab597838e4abbe8de30d7224afd062ea
//
//   - pastas: seções [Bookmarks] (raiz) e [Bookmarks_N], com SubRep=Pasta\Subpasta
//   - sessão: Nome=#ícone#tipo%host%porta%usuário%...%chave(14)%...#...
//   - tipo 0 é SSH; o resto (RDP, VNC, SFTP…) fica de fora e é contado em skipped
//
// Senhas não vêm no arquivo (o MobaXterm guarda criptografadas à parte), então
// os servidores entram como "automático": o ssh pergunta a senha ao conectar.
// O caminho da chave privada é devolvido em KeyPath, do jeito que o MobaXterm
// grava (caminho do Windows); quem chama decide se consegue ler o arquivo.
func MobaXterm(data []byte) (items []Item, skipped int, err error) {
	if !utf8.Valid(data) {
		data = decodeCP1252(data)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	used := map[string]bool{}
	inBookmarks, sawBookmarks := false, false
	folder := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sec := trimmed[1 : len(trimmed)-1]
			inBookmarks = sec == "Bookmarks" || strings.HasPrefix(sec, "Bookmarks_")
			sawBookmarks = sawBookmarks || inBookmarks
			folder = ""
			continue
		}
		if !inBookmarks || trimmed == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "SubRep":
			folder = mobaFolder(value)
			continue
		case "ImgNum":
			continue
		}

		// Nome=#ícone#campos%...#...
		parts := strings.Split(value, "#")
		if len(parts) < 3 {
			continue
		}
		f := strings.Split(parts[2], "%")
		if f[0] != "0" || len(f) < 2 || strings.TrimSpace(f[1]) == "" {
			skipped++ // não é SSH (RDP, VNC, SFTP…) ou não tem host
			continue
		}
		field := func(i int) string {
			if i < len(f) {
				return strings.TrimSpace(unescapeMoba(f[i]))
			}
			return ""
		}

		name := strings.TrimSpace(unescapeMoba(key))
		if name == "" {
			name = field(1)
		}
		base := name
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		used[name] = true

		it := Item{Host: config.Host{
			Name:   name,
			Folder: folder,
			Host:   field(1),
			User:   field(3),
			Auth:   config.AuthAuto,
		}}
		if p, err := strconv.Atoi(field(2)); err == nil && p != 22 && p > 0 && p < 65536 {
			it.Host.Port = p
		}
		if k := field(14); k != "" {
			it.KeyPath = mobaPath(k)
		}
		items = append(items, it)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	if !sawBookmarks {
		return nil, 0, fmt.Errorf("não parece um arquivo do MobaXterm (sem seção [Bookmarks])")
	}
	return items, skipped, nil
}

// mobaFolder converte "Pasta\Subpasta" em "Pasta/Subpasta".
func mobaFolder(subrep string) string {
	var out []string
	for _, p := range strings.Split(unescapeMoba(subrep), `\`) {
		if p = clean(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// mobaPath desfaz os marcadores de caminho do MobaXterm (_CurrentDrive_ = "C").
func mobaPath(p string) string {
	return strings.ReplaceAll(p, "_CurrentDrive_", "C")
}

var mobaEscapes = strings.NewReplacer(
	"__PTVIRG__", ";",
	"__DBLQUO__", `"`,
	"__PIPE__", "|",
	"__DIEZE__", "#",
	"__PERCENT__", "%",
)

func unescapeMoba(s string) string { return mobaEscapes.Replace(s) }

// cp1252 são os caracteres de 0x80 a 0x9F do Windows-1252 (o resto é igual ao Latin-1).
var cp1252 = [32]rune{
	'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f',
	'\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ',
}

// decodeCP1252 converte o texto em Windows-1252 (como o MobaXterm grava) pra UTF-8.
func decodeCP1252(b []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/8)
	for _, c := range b {
		switch {
		case c < 0x80:
			out.WriteByte(c)
		case c < 0xA0:
			out.WriteRune(cp1252[c-0x80])
		default:
			out.WriteRune(rune(c))
		}
	}
	return out.Bytes()
}
