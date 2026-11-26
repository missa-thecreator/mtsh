package importer

import "testing"

func TestElecterm(t *testing.T) {
	data := []byte(`{
	  "bookmarks": [
	    {"id":"a","title":"web ","host":"10.0.0.1","username":"root","authType":"password","password":"s3cret","port":22},
	    {"id":"b","title":"web","host":"10.0.0.2","username":"root","port":2222},
	    {"id":"c","title":"win","host":"10.0.0.3","type":"rdp","port":3389},
	    {"id":"d","title":"key","host":"10.0.0.4","type":"ssh","authType":"privateKey","privateKey":"-----BEGIN KEY-----"},
	    {"id":"e","title":"solto","host":"10.0.0.5"}
	  ],
	  "bookmarkGroups": [
	    {"id":"default","title":"default","bookmarkIds":["b"]},
	    {"id":"g1","title":"Prod/Web","bookmarkIds":["a"],"bookmarkGroupIds":["g2"]},
	    {"id":"g2","title":"Chaves","bookmarkIds":["d"]}
	  ]
	}`)
	items, skipped, err := Electerm(data)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(items) != 4 {
		t.Fatalf("skipped=%d items=%d", skipped, len(items))
	}
	want := []struct{ name, folder, auth string }{
		{"web", "Prod-Web", "password"},
		{"web (2)", "", "auto"},
		{"key", "Prod-Web/Chaves", "key"},
		{"solto", "", "auto"},
	}
	for i, w := range want {
		h := items[i].Host
		if h.Name != w.name || h.Folder != w.folder || h.Auth != w.auth {
			t.Errorf("item %d = %+v, quer %+v", i, h, w)
		}
	}
	if items[0].Password != "s3cret" || items[1].Host.Port != 2222 || items[2].Key == "" {
		t.Errorf("campos errados: %+v", items)
	}
	if _, _, err := Electerm([]byte(`{"x":1}`)); err == nil {
		t.Error("devia recusar JSON sem bookmarks")
	}
}
