package importer

import "testing"

// Servidor com host ou usuário começando com "-" é pulado na importação.
func TestDetectSkipsOptionLikeTargets(t *testing.T) {
	data := []byte(`{"bookmarks": [
	  {"id":"a","title":"ok","host":"10.0.0.1","username":"root"},
	  {"id":"b","title":"mau","host":"-oProxyCommand=sh -c id","username":"root"},
	  {"id":"c","title":"mau2","host":"10.0.0.3","username":"-oProxyCommand=id"}
	]}`)
	items, skipped, _, err := Detect(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Host.Name != "ok" || skipped != 2 {
		t.Fatalf("items=%v skipped=%d", items, skipped)
	}
}
