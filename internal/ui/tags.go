package ui

import (
	"hash/fnv"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"mtsh/internal/config"
)

// parseTags lê o campo de tags: separa por vírgula, deixa minúsculo, troca
// espaço por hífen e tira repetidas ("Prod, banco de dados" → prod, banco-de-dados).
func parseTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.Join(strings.Fields(strings.ToLower(t)), "-")
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// allTags são todas as tags em uso, em ordem alfabética.
func allTags(cfg *config.File) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range cfg.Hosts {
		for _, t := range h.Tags {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// tagColor dá sempre a mesma cor pra mesma tag (paleta das pastas, sem a padrão).
func tagColor(tag string) lipgloss.Color {
	h := fnv.New32a()
	h.Write([]byte(tag))
	palette := folderColors[1:]
	return palette[int(h.Sum32()%uint32(len(palette)))].color
}

func tagStyle(tag string) lipgloss.Style { return lipgloss.NewStyle().Foreground(tagColor(tag)) }

// suggestTag sugere uma tag existente que começa com o que está sendo digitado
// (o trecho depois da última vírgula). Devolve "" se não houver sugestão.
func suggestTag(input string, existing []string) string {
	parts := strings.Split(input, ",")
	token := strings.Join(strings.Fields(strings.ToLower(parts[len(parts)-1])), "-")
	if token == "" {
		return ""
	}
	typed := map[string]bool{}
	for _, t := range parseTags(strings.Join(parts[:len(parts)-1], ",")) {
		typed[t] = true
	}
	for _, t := range existing {
		if t != token && strings.HasPrefix(t, token) && !typed[t] {
			return t
		}
	}
	return ""
}

// completeTag troca o trecho sendo digitado pela sugestão e já abre a próxima.
func completeTag(input, tag string) string {
	i := strings.LastIndex(input, ",")
	prefix := ""
	if i >= 0 {
		prefix = strings.TrimRight(input[:i+1], " ") + " "
	}
	return prefix + tag + ", "
}

// tagSpan diz em que colunas (dentro do conteúdo do card) uma tag foi desenhada.
type tagSpan struct {
	tag        string
	start, end int
}

// cardTagsLine monta a linha "Ubuntu 24.04 · prod  banco" do card e devolve
// onde cada tag ficou, pra saber em qual tag a pessoa clicou.
func cardTagsLine(h config.Host, width int) (string, []tagSpan) {
	var b strings.Builder
	var spans []tagSpan
	pos := 0
	if os := osLabel(h); os != "" {
		b.WriteString(os)
		pos += lipgloss.Width(os)
	}
	for i, t := range h.Tags {
		sep := "  "
		if i == 0 && pos > 0 {
			sep = " · "
		} else if i == 0 {
			sep = ""
		}
		w := runewidth.StringWidth(t)
		sw := runewidth.StringWidth(sep) // largura na tela ("·" tem 2 bytes, 1 coluna)
		if pos+sw+w > width {
			if pos+sw+1 <= width {
				b.WriteString(sFaint.Render(sep + "…"))
			}
			break
		}
		b.WriteString(sFaint.Render(sep))
		pos += sw
		spans = append(spans, tagSpan{t, pos, pos + w})
		b.WriteString(tagStyle(t).Render(t))
		pos += w
	}
	if pos == 0 {
		return sFaint.Render("sistema aparece ao conectar"), nil
	}
	return b.String(), spans
}

// hostHasTag diz se algum servidor tem exatamente essa tag.
func hostHasTag(cfg *config.File, tag string) bool {
	for _, h := range cfg.Hosts {
		for _, t := range h.Tags {
			if t == tag {
				return true
			}
		}
	}
	return false
}

// matchTag: na busca "#tag", vale a tag exata se ela existir; enquanto a
// pessoa digita (ex: "#pr"), vale o começo da tag.
func matchTag(h config.Host, tag string, exact bool) bool {
	for _, t := range h.Tags {
		if (exact && t == tag) || (!exact && strings.HasPrefix(t, tag)) {
			return true
		}
	}
	return false
}
