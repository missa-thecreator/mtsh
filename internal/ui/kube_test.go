package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/kube"
	"mtsh/internal/session"
)

// kubeTestModel é um Model sem kubectl de verdade (ele leria o kubeconfig de
// quem roda o teste) e com um shell que não faz nada.
func kubeTestModel(t *testing.T) *Model {
	old, oldShell := kube.Bin, os.Getenv("SHELL")
	kube.Bin = "true"
	os.Setenv("SHELL", "true")
	t.Cleanup(func() { kube.Bin = old; os.Setenv("SHELL", oldShell) })
	m := dbTestModel(t)
	m.kubeSess = map[*session.Session]*kubeSession{}
	m.kubeVis = map[*session.Session]*kubeView{}
	return m
}

func TestClusterAbreNoTerminalEAltV(t *testing.T) {
	m := kubeTestModel(t)
	m.openKube(kube.Context{Name: "lab"})
	s := m.current()
	if s == nil || m.kubeSess[s] == nil || !m.kubeSess[s].term {
		t.Fatal("o cluster devia abrir numa aba de terminal")
	}
	k := m.kubeSess[s]
	data, err := os.ReadFile(k.kubeconfig)
	if err != nil || !strings.Contains(string(data), `current-context: "lab"`) {
		t.Fatalf("kubeconfig provisório: %q %v", data, err)
	}
	if m.currentKube() != nil {
		t.Fatal("começa no terminal, não na visualização")
	}
	m.sessionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v"), Alt: true})
	if v := m.currentKube(); v == nil || v.kctx.Name != "lab" {
		t.Fatal("alt+v devia mostrar a visualização")
	}
	m.sessionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v"), Alt: true})
	if m.currentKube() != nil {
		t.Fatal("alt+v de novo devia voltar pro terminal")
	}
	// abrir o mesmo cluster de novo só volta pra aba
	m.tab = 0
	m.openKube(kube.Context{Name: "lab"})
	if len(m.sessions) != 1 || m.current() != s {
		t.Fatal("abriu outra aba pro mesmo cluster")
	}
	m.closeTab(1)
	if _, err := os.Stat(k.kubeconfig); !os.IsNotExist(err) {
		t.Fatal("o kubeconfig provisório devia ser apagado ao fechar a aba")
	}
}

// fechar a aba de logs/shell de um pod volta pra visualização do cluster, não pro Início
func TestFecharAbaDePodVoltaProCluster(t *testing.T) {
	m := kubeTestModel(t)
	m.openKube(kube.Context{Name: "lab"})
	cluster := m.current()
	m.startKubeSession(kubeSession{kctx: "lab", ns: "app", pod: "web-1", logs: true})
	if m.current() == cluster {
		t.Fatal("os logs deviam abrir em outra aba")
	}
	m.closeTab(m.tab)
	if m.current() != cluster || m.currentKube() == nil {
		t.Fatalf("depois de fechar os logs foi pra aba %d, quero a visualização do cluster", m.tab)
	}
	// sem a aba do cluster: aí sim volta pro Início
	m.closeTab(m.indexOf(cluster))
	m.startKubeSession(kubeSession{kctx: "lab", ns: "app", pod: "web-1"})
	m.closeTab(m.tab)
	if m.tab != 0 {
		t.Fatalf("sem a aba do cluster devia ir pro Início, foi pra %d", m.tab)
	}
}
