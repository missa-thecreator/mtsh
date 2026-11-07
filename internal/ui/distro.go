package ui

import (
	"github.com/charmbracelet/lipgloss"

	"mtsh/internal/config"
)

// distro: logo (Nerd Font) e cor da marca de cada sistema.
type distro struct {
	glyph string
	color lipgloss.Color
}

var distros = map[string]distro{
	"ubuntu":              {"", "#E95420"},
	"debian":              {"", "#D70A53"},
	"raspbian":            {"", "#C51A4A"},
	"linuxmint":           {"", "#87CF3E"},
	"pop":                 {"", "#48B9C7"},
	"elementary":          {"", "#64BAFF"},
	"zorin":               {"", "#15A6F0"},
	"kali":                {"", "#557C94"},
	"parrot":              {"", "#05C5B5"},
	"deepin":              {"", "#0050FF"},
	"fedora":              {"", "#51A2DA"},
	"rhel":                {"", "#EE0000"},
	"centos":              {"", "#A14F8C"},
	"rocky":               {"", "#10B981"},
	"almalinux":           {"", "#0F4266"},
	"amzn":                {"", "#FF9900"},
	"arch":                {"", "#1793D1"},
	"manjaro":             {"", "#35BF5C"},
	"endeavouros":         {"", "#7F7FFF"},
	"artix":               {"", "#10A0CC"},
	"alpine":              {"", "#2F7FBF"},
	"opensuse":            {"", "#73BA25"},
	"opensuse-leap":       {"", "#73BA25"},
	"opensuse-tumbleweed": {"", "#73BA25"},
	"sles":                {"", "#73BA25"},
	"gentoo":              {"", "#7A6FAC"},
	"nixos":               {"", "#7EBAE4"},
	"void":                {"", "#478061"},
	"slackware":           {"", "#6E8FC9"},
	"solus":               {"", "#5294E2"},
	"freebsd":             {"", "#AB2B28"},
	"openbsd":             {"", "#F2CA30"},
	"macos":               {"", "#A2AAAD"},
	"linux":               {"", "#FCC624"},
}

// distroOf acha a distro do host: pelo ID, senão pela família, senão Linux genérico.
func distroOf(h config.Host) (distro, bool) {
	if h.OS == "" {
		return distro{}, false
	}
	if d, ok := distros[h.OS]; ok {
		return d, true
	}
	if d, ok := distros[h.OSLike]; ok {
		return d, true
	}
	return distros["linux"], true
}

// searchIcon é a lupa da busca: a da Nerd Font quando os logos estão ligados
// (mesma fonte), senão "/" — que existe em qualquer fonte.
func (m *Model) searchIcon() string {
	if m.cfg.SimpleIcons {
		return "/"
	}
	return "\uf002"
}

// iconServer é o ícone (Nerd Font) de servidor ainda não detectado.
const iconServer = "\uf233"

// hostMark é o símbolo do host: o logo da distro (Nerd Font). Antes da
// primeira conexão, um ícone de servidor apagado. Com simple_icons, vira
// uma bolinha na cor da distro.
func (m *Model) hostMark(h config.Host) string {
	d, ok := distroOf(h)
	switch {
	case !ok && m.cfg.SimpleIcons:
		return sFaint.Render("○")
	case !ok:
		return sFaint.Render(iconServer)
	case m.cfg.SimpleIcons:
		return lipgloss.NewStyle().Foreground(d.color).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(d.color).Render(d.glyph)
	}
}

// markChar é o símbolo do host sem cor (pra montar linhas com estilo único).
func (m *Model) markChar(h config.Host) string {
	d, ok := distroOf(h)
	switch {
	case !ok && m.cfg.SimpleIcons:
		return "○"
	case !ok:
		return iconServer
	case m.cfg.SimpleIcons:
		return "●"
	}
	return d.glyph
}

// osLabel é o nome do sistema na cor da distro (vazio se não detectado).
func osLabel(h config.Host) string {
	d, ok := distroOf(h)
	if !ok || h.OSName == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(d.color).Render(h.OSName)
}
