// Package clipboard copia e cola pela área de transferência do sistema.
//
// No WSL usa o PowerShell do Windows (é lá que fica a área de transferência
// do Windows Terminal e do VS Code). No Linux usa wl-copy/wl-paste (Wayland)
// ou xclip/xsel (X11), e no macOS pbcopy/pbpaste. Se nada disso existir (ex:
// o mtsh rodando num servidor via ssh), Copy ainda tenta o OSC 52: uma
// sequência que pede pro terminal de fora copiar o texto.
package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable: nenhuma forma de ler a área de transferência neste sistema.
var ErrUnavailable = errors.New("não achei como acessar a área de transferência")

const timeout = 10 * time.Second

// Copy coloca text na área de transferência. Se não houver ferramenta do
// sistema, manda o OSC 52 pro terminal (out) e devolve nil: não tem como
// saber se o terminal aceitou.
func Copy(text string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := copyNative(ctx, text)
	if errors.Is(err, ErrUnavailable) && out != nil {
		_, err = io.WriteString(out, OSC52(text))
	}
	return err
}

// Paste lê o texto da área de transferência.
func Paste() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return pasteNative(ctx)
}

// OSC52 é a sequência que pede pro terminal copiar text.
func OSC52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// ---- ferramentas do sistema ----

func copyNative(ctx context.Context, text string) error {
	switch {
	case isWSL():
		ps, err := powershell()
		if err != nil {
			return err
		}
		// o texto vai em base64 pelo stdin: assim acentos e emojis chegam
		// inteiros, sem depender da página de código do console do Windows
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
		in := base64.StdEncoding.EncodeToString([]byte(text))
		return run(ctx, in, ps, "-NoProfile", "-NonInteractive", "-Command",
			"$b=[Console]::In.ReadToEnd(); Set-Clipboard -Value ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b.Trim())))")
	case runtime.GOOS == "darwin" && has("pbcopy"):
		return run(ctx, text, "pbcopy")
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-copy"):
		return run(ctx, text, "wl-copy")
	case os.Getenv("DISPLAY") != "" && has("xclip"):
		return run(ctx, text, "xclip", "-selection", "clipboard")
	case os.Getenv("DISPLAY") != "" && has("xsel"):
		return run(ctx, text, "xsel", "--clipboard", "--input")
	}
	return ErrUnavailable
}

func pasteNative(ctx context.Context) (string, error) {
	switch {
	case isWSL():
		ps, err := powershell()
		if err != nil {
			return "", err
		}
		out, err := output(ctx, ps, "-NoProfile", "-NonInteractive", "-Command",
			"$t=Get-Clipboard -Raw; if ($t) { [Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($t))) }")
		if err != nil {
			return "", err
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
		if err != nil {
			return "", fmt.Errorf("resposta estranha do PowerShell: %w", err)
		}
		return string(b), nil
	case runtime.GOOS == "darwin" && has("pbpaste"):
		return output(ctx, "pbpaste")
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-paste"):
		return output(ctx, "wl-paste", "--no-newline")
	case os.Getenv("DISPLAY") != "" && has("xclip"):
		return output(ctx, "xclip", "-selection", "clipboard", "-o")
	case os.Getenv("DISPLAY") != "" && has("xsel"):
		return output(ctx, "xsel", "--clipboard", "--output")
	}
	return "", ErrUnavailable
}

func has(name string) bool { _, err := exec.LookPath(name); return err == nil }

func run(ctx context.Context, stdin, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// powershell acha o powershell.exe, mesmo com o PATH do Windows desligado no WSL.
func powershell() (string, error) {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, nil
	}
	p := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", ErrUnavailable
}

// isWSL: mesma checagem do pacote fonts.
func isWSL() bool {
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		return true
	}
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}
