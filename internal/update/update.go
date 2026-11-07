// Package update descobre se saiu versão nova do mtsh (releases do GitHub)
// e troca o binário pela versão nova, conferindo o checksum.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultRepo = "missa-thecreator/mtsh"

// Release é a última versão publicada.
type Release struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func repo() string {
	if r := os.Getenv("MTSH_REPO"); r != "" {
		return r
	}
	return defaultRepo
}

func apiBase() string {
	if b := os.Getenv("MTSH_GITHUB_API"); b != "" {
		return strings.TrimSuffix(b, "/")
	}
	return "https://api.github.com"
}

var client = &http.Client{Timeout: 60 * time.Second}

// Latest consulta a última release publicada.
func Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase()+"/repos/"+repo()+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub respondeu %s", resp.Status)
	}
	var r Release
	return r, json.NewDecoder(resp.Body).Decode(&r)
}

// Newer diz se a versão latest é mais nova que current (formato vX.Y.Z).
// Versões que não seguem o formato (ex: "dev") nunca são comparadas.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// ---- consulta com cache (no máximo uma vez por dia) ----

type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mtsh", "update.json")
}

// Check devolve a versão mais nova disponível, ou "" se a atual já é a última.
// Usa o resultado guardado se a última consulta tiver menos de um dia.
func Check(ctx context.Context, current string) (string, error) {
	if os.Getenv("MTSH_NO_UPDATE_CHECK") != "" {
		return "", nil
	}
	if _, ok := parse(current); !ok {
		return "", nil // versão de desenvolvimento
	}
	path := cachePath()
	var c cache
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &c) == nil && time.Since(c.CheckedAt) < 24*time.Hour {
		if Newer(current, c.Latest) {
			return c.Latest, nil
		}
		return "", nil
	}
	r, err := Latest(ctx)
	if err != nil {
		return "", err
	}
	if path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if b, err := json.Marshal(cache{time.Now(), r.Tag}); err == nil {
			os.WriteFile(path, b, 0o644)
		}
	}
	if Newer(current, r.Tag) {
		return r.Tag, nil
	}
	return "", nil
}

// ---- aplicar ----

// ErrDev: rodando uma versão de desenvolvimento (go run), sem o que atualizar.
var ErrDev = errors.New("esta é uma versão de desenvolvimento; atualize pelo git ou pelo instalador")

// Apply baixa a última versão e troca o binário em execução.
// Devolve a versão instalada ("" se já estava na última).
func Apply(ctx context.Context, current string, out io.Writer) (string, error) {
	if _, ok := parse(current); !ok {
		return "", ErrDev
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	return ApplyTo(ctx, current, exe, out)
}

// ApplyTo é o Apply com o caminho do binário explícito (usado nos testes).
func ApplyTo(ctx context.Context, current, target string, out io.Writer) (string, error) {
	r, err := Latest(ctx)
	if err != nil {
		return "", fmt.Errorf("não consegui consultar a última versão: %w", err)
	}
	if !Newer(current, r.Tag) {
		fmt.Fprintf(out, "✓ você já está na última versão (%s)\n", current)
		return "", nil
	}

	suffix := fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var pkgURL, pkgName, sumsURL string
	for _, a := range r.Assets {
		switch {
		case strings.HasSuffix(a.Name, suffix):
			pkgURL, pkgName = a.URL, a.Name
		case a.Name == "checksums.txt":
			sumsURL = a.URL
		}
	}
	if pkgURL == "" {
		return "", fmt.Errorf("a versão %s não tem pacote pra %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}
	if sumsURL == "" {
		return "", fmt.Errorf("a versão %s não tem checksums.txt; não dá pra conferir o download", r.Tag)
	}

	fmt.Fprintf(out, "→ baixando %s\n", pkgName)
	pkg, err := download(ctx, pkgURL)
	if err != nil {
		return "", err
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return "", err
	}
	if err := verify(pkg, pkgName, sums); err != nil {
		return "", err
	}
	fmt.Fprintln(out, "✓ checksum confere")

	bin, err := extract(pkg)
	if err != nil {
		return "", err
	}
	if err := replace(target, bin); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "✓ mtsh %s instalado em %s\n", r.Tag, target)
	if r.URL != "" {
		fmt.Fprintf(out, "  novidades: %s\n", r.URL)
	}
	return r.Tag, nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha ao baixar %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("falha ao baixar %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func verify(pkg []byte, name string, sums []byte) error {
	sum := sha256.Sum256(pkg)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if f[0] == got {
				return nil
			}
			return errors.New("checksum não confere; o download pode estar corrompido")
		}
	}
	return fmt.Errorf("%s não aparece no checksums.txt", name)
}

// extract pega o binário "mtsh" de dentro do .tar.gz.
func extract(pkg []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(pkg))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("o pacote não tem o binário mtsh")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "mtsh" {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// replace troca o binário: grava ao lado e renomeia por cima (atômico).
// No Linux e no macOS dá pra fazer isso com o programa rodando.
func replace(target string, bin []byte) error {
	tmp := target + ".novo"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("sem permissão pra escrever em %s; rode o instalador de novo ou use sudo", filepath.Dir(target))
		}
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
