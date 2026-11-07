// Package osinfo descobre qual sistema roda num servidor a partir da saída
// de "cat /etc/os-release; uname -s".
package osinfo

import (
	"strings"
)

// Info é o sistema detectado.
type Info struct {
	ID   string // ubuntu, debian, alpine, macos, freebsd…
	Like string // família (ID_LIKE), ex: "debian" no Ubuntu
	Name string // nome curto pra mostrar, ex: "Ubuntu 24.04"
}

// Command é o que roda no servidor pra detectar o sistema.
const Command = "cat /etc/os-release 2>/dev/null; echo; uname -s 2>/dev/null"

// Parse lê a saída de Command.
func Parse(out string) (Info, bool) {
	kv := map[string]string{}
	var lastLine string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastLine = line
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[k] = strings.Trim(v, `"'`)
		}
	}

	if id := strings.ToLower(kv["ID"]); id != "" {
		name := kv["NAME"]
		// nomes longos ficam curtos: "Ubuntu" em vez de "Ubuntu GNU/Linux"
		name = strings.TrimSuffix(strings.TrimSuffix(name, " GNU/Linux"), " Linux")
		if name == "" {
			name = id
		}
		if v := kv["VERSION_ID"]; v != "" {
			name += " " + v
		}
		like := strings.Fields(strings.ToLower(kv["ID_LIKE"]))
		info := Info{ID: id, Name: name}
		if len(like) > 0 {
			info.Like = like[0]
		}
		return info, true
	}

	// sem os-release: usa o uname
	switch strings.ToLower(lastLine) {
	case "darwin":
		return Info{ID: "macos", Name: "macOS"}, true
	case "freebsd":
		return Info{ID: "freebsd", Name: "FreeBSD"}, true
	case "openbsd":
		return Info{ID: "openbsd", Name: "OpenBSD"}, true
	case "linux":
		return Info{ID: "linux", Name: "Linux"}, true
	}
	return Info{}, false
}
