package importer

import (
	"bytes"
	"fmt"
)

// Detect descobre de qual programa é o arquivo e lê os servidores dele.
// source é o nome do programa, pra mostrar na mensagem.
func Detect(data []byte) (items []Item, skipped int, source string, err error) {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	switch {
	case bytes.HasPrefix(trimmed, []byte("{")):
		items, skipped, err = Electerm(data)
		return items, skipped, "electerm", err
	case bytes.Contains(data, []byte("[Bookmarks")):
		items, skipped, err = MobaXterm(data)
		return items, skipped, "MobaXterm", err
	}
	return nil, 0, "", fmt.Errorf("não reconheci o arquivo: use o JSON exportado pelo electerm ou o .mxtsessions / MobaXterm.ini do MobaXterm")
}
