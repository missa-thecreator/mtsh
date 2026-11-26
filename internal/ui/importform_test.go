package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportBrowse(t *testing.T) {
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "sub"), 0o755)
	os.WriteFile(filepath.Join(d, "a.mxtsessions"), nil, 0o644)
	os.WriteFile(filepath.Join(d, "b.json"), nil, 0o644)

	im := newImportModal()
	im.path.SetValue(d + "/")
	im.browse()
	names := func() (out []string) {
		for _, e := range im.entries {
			out = append(out, e.name)
		}
		return
	}
	if got := names(); len(got) != 4 || got[0] != ".." || got[1] != "sub" {
		t.Fatalf("lista = %v, quer .. e a pasta primeiro", got)
	}
	im.path.SetValue(d + "/A")
	im.browse()
	if got := names(); len(got) != 1 || got[0] != "a.mxtsessions" {
		t.Fatalf("filtro = %v", got)
	}
	im.path.SetValue(d + "/")
	im.browse()
	im.open(nil, 1) // entra em sub
	if im.path.Value() != d+"/sub/" {
		t.Fatalf("depois de abrir sub: %q", im.path.Value())
	}
	im.open(nil, 0) // ..
	if im.path.Value() != d+"/" {
		t.Fatalf("depois de ..: %q", im.path.Value())
	}
}
