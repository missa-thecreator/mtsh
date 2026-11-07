package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.2.0", "v0.2.1", true},
		{"v0.2.0", "v0.10.0", true},
		{"v0.2.1", "v0.2.1", false},
		{"v0.3.0", "v0.2.9", false},
		{"v1.0.0", "v0.9.9", false},
		{"dev", "v9.9.9", false},
		{"v0.2.0", "lixo", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, quero %v", c.cur, c.latest, got, c.want)
		}
	}
}

// fakeGitHub serve uma release com um pacote contendo o binário "novo".
func fakeGitHub(t *testing.T, tag string, bin []byte, badSum bool) {
	t.Helper()
	name := fmt.Sprintf("mtsh_%s_%s_%s.tar.gz", tag[1:], runtime.GOOS, runtime.GOARCH)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pasta/mtsh", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	tw.Write(bin)
	tw.Close()
	gz.Close()
	pkg := buf.Bytes()
	sum := sha256.Sum256(pkg)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if badSum {
		sums = "00" + sums[2:]
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/x/mtsh/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": tag, "html_url": "https://exemplo/release",
				"assets": []map[string]string{
					{"name": name, "browser_download_url": srv.URL + "/dl/" + name},
					{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/checksums.txt"},
				},
			})
		case "/dl/" + name:
			w.Write(pkg)
		case "/dl/checksums.txt":
			io.WriteString(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MTSH_GITHUB_API", srv.URL)
	t.Setenv("MTSH_REPO", "x/mtsh")
}

func TestApplyTroca(t *testing.T) {
	fakeGitHub(t, "v0.3.0", []byte("binario-novo"), false)
	target := filepath.Join(t.TempDir(), "mtsh")
	os.WriteFile(target, []byte("binario-velho"), 0o755)

	var out bytes.Buffer
	got, err := ApplyTo(context.Background(), "v0.2.0", target, &out)
	if err != nil || got != "v0.3.0" {
		t.Fatalf("ApplyTo = %q, %v\n%s", got, err, out.String())
	}
	if b, _ := os.ReadFile(target); string(b) != "binario-novo" {
		t.Errorf("binário não foi trocado: %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm()&0o100 == 0 {
		t.Error("binário novo precisa ser executável")
	}
}

func TestApplyJaAtualizado(t *testing.T) {
	fakeGitHub(t, "v0.2.0", []byte("x"), false)
	target := filepath.Join(t.TempDir(), "mtsh")
	os.WriteFile(target, []byte("atual"), 0o755)
	if got, err := ApplyTo(context.Background(), "v0.2.0", target, io.Discard); err != nil || got != "" {
		t.Fatalf("já atualizado: %q, %v", got, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "atual" {
		t.Error("não deveria mexer no binário")
	}
}

func TestApplyChecksumErrado(t *testing.T) {
	fakeGitHub(t, "v0.3.0", []byte("binario-adulterado"), true)
	target := filepath.Join(t.TempDir(), "mtsh")
	os.WriteFile(target, []byte("binario-velho"), 0o755)
	if _, err := ApplyTo(context.Background(), "v0.2.0", target, io.Discard); err == nil {
		t.Fatal("checksum errado deveria falhar")
	}
	if b, _ := os.ReadFile(target); string(b) != "binario-velho" {
		t.Error("com checksum errado o binário não pode mudar")
	}
}

func TestCheckUsaCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	fakeGitHub(t, "v0.3.0", []byte("x"), false)
	if got, _ := Check(context.Background(), "v0.2.0"); got != "v0.3.0" {
		t.Fatalf("Check = %q", got)
	}
	// sem servidor, a segunda consulta vem do cache
	t.Setenv("MTSH_GITHUB_API", "http://127.0.0.1:1")
	if got, err := Check(context.Background(), "v0.2.0"); got != "v0.3.0" || err != nil {
		t.Fatalf("Check com cache = %q, %v", got, err)
	}
	if got, _ := Check(context.Background(), "dev"); got != "" {
		t.Error("versão de desenvolvimento não deveria consultar")
	}
}
