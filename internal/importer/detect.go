package importer

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// Detect descobre de qual programa é o arquivo e lê os servidores dele.
// source é o nome do programa, pra mostrar na mensagem.
func Detect(data []byte) (items []Item, skipped int, source string, err error) {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	switch {
	case bytes.HasPrefix(trimmed, []byte("{")):
		items, skipped, err = Electerm(data)
		source = "electerm"
	case bytes.Contains(data, []byte("[Bookmarks")):
		items, skipped, err = MobaXterm(data)
		source = "MobaXterm"
	default:
		return nil, 0, "", fmt.Errorf("não reconheci o arquivo: use o JSON exportado pelo electerm ou o .mxtsessions / MobaXterm.ini do MobaXterm")
	}
	// o arquivo veio de outra pessoa: host ou usuário começando com "-" seria
	// lido como opção do ssh, então nem entra
	n := len(items)
	items = slices.DeleteFunc(items, func(it Item) bool {
		return strings.HasPrefix(it.Host.Host, "-") || strings.HasPrefix(it.Host.User, "-")
	})
	return items, skipped + n - len(items), source, err
}
