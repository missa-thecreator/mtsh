// Package kube conversa com clusters Kubernetes pelo kubectl instalado na
// máquina, do mesmo jeito que o mtsh usa o ssh do sistema: os clusters vêm do
// kubeconfig de sempre (~/.kube/config ou $KUBECONFIG) e qualquer forma de
// login que o kubectl aceita (certificado, token, plugins de nuvem) funciona.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Bin é o kubectl a usar (dá pra trocar nos testes).
var Bin = "kubectl"

// Available diz se o kubectl está instalado.
func Available() bool {
	_, err := exec.LookPath(Bin)
	return err == nil
}

// Context é um cluster do kubeconfig (um "contexto": cluster + usuário + namespace).
type Context struct {
	Name      string
	Cluster   string
	Server    string // endereço da API
	User      string
	Namespace string // namespace padrão do contexto ("" = default)
	Current   bool   // o contexto atual do kubectl
}

// run roda o kubectl e devolve a saída; o erro traz a mensagem do kubectl.
func run(ctx context.Context, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	c := exec.CommandContext(ctx, Bin, args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(firstLine(msg))
	}
	return out.Bytes(), nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimPrefix(s, "error: ")
}

// base são os argumentos que todo comando leva: o contexto e um tempo limite
// (cluster fora do ar não pode travar a tela).
func base(kctx string) []string {
	return []string{"--context", kctx, "--request-timeout=15s"}
}

// Contexts lê os contextos do kubeconfig. Não fala com nenhum cluster, e o
// kubectl esconde os certificados e tokens dessa saída.
func Contexts(ctx context.Context) ([]Context, error) {
	out, err := run(ctx, "config", "view", "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseContexts(out)
}

func parseContexts(data []byte) ([]Context, error) {
	var cfg struct {
		Current  string `json:"current-context"`
		Contexts []struct {
			Name    string `json:"name"`
			Context struct {
				Cluster   string `json:"cluster"`
				User      string `json:"user"`
				Namespace string `json:"namespace"`
			} `json:"context"`
		} `json:"contexts"`
		Clusters []struct {
			Name    string `json:"name"`
			Cluster struct {
				Server string `json:"server"`
			} `json:"cluster"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("kubeconfig ilegível: %w", err)
	}
	servers := map[string]string{}
	for _, c := range cfg.Clusters {
		servers[c.Name] = c.Cluster.Server
	}
	var out []Context
	for _, c := range cfg.Contexts {
		out = append(out, Context{
			Name: c.Name, Cluster: c.Context.Cluster, Server: servers[c.Context.Cluster],
			User: c.Context.User, Namespace: c.Context.Namespace, Current: c.Name == cfg.Current,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Namespaces lista os namespaces do cluster.
func Namespaces(ctx context.Context, kctx string) ([]string, error) {
	out, err := run(ctx, append(base(kctx), "get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}")...)
	if err != nil {
		return nil, err
	}
	ns := strings.Fields(string(out))
	sort.Strings(ns)
	return ns, nil
}

// Pod é uma linha da lista de pods, já com o resumo que o kubectl get pods mostra.
type Pod struct {
	Namespace  string
	Name       string
	Ready      string // prontos/total, ex: 1/1
	Status     string // Running, CrashLoopBackOff, Completed…
	Restarts   int
	Created    time.Time
	Node       string
	Containers []string
}

// Healthy diz se o pod está bem (pra pintar de verde ou de vermelho).
func (p Pod) Healthy() bool {
	switch p.Status {
	case "Running":
		r, t, _ := strings.Cut(p.Ready, "/")
		return r == t
	case "Completed", "Succeeded":
		return true
	}
	return false
}

// Pods lista os pods do namespace ("" = todos os namespaces).
func Pods(ctx context.Context, kctx, ns string) ([]Pod, error) {
	args := append(base(kctx), "get", "pods", "-o", "json")
	if ns == "" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", ns)
	}
	out, err := run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parsePods(out)
}

type containerStatus struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
		Terminated *struct {
			Reason   string `json:"reason"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated"`
	} `json:"state"`
}

func parsePods(data []byte) ([]Pod, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string     `json:"name"`
				Namespace         string     `json:"namespace"`
				CreationTimestamp time.Time  `json:"creationTimestamp"`
				DeletionTimestamp *time.Time `json:"deletionTimestamp"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase                 string            `json:"phase"`
				Reason                string            `json:"reason"`
				InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
				ContainerStatuses     []containerStatus `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("resposta inesperada do kubectl: %w", err)
	}
	pods := make([]Pod, 0, len(list.Items))
	for _, it := range list.Items {
		p := Pod{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Created: it.Metadata.CreationTimestamp,
			Node: it.Spec.NodeName}
		for _, c := range it.Spec.Containers {
			p.Containers = append(p.Containers, c.Name)
		}
		ready := 0
		for _, c := range it.Status.ContainerStatuses {
			if c.Ready {
				ready++
			}
			p.Restarts += c.RestartCount
		}
		p.Ready = fmt.Sprintf("%d/%d", ready, len(it.Spec.Containers))
		p.Status = podStatus(it.Status.Phase, it.Status.Reason, it.Status.InitContainerStatuses,
			it.Status.ContainerStatuses, it.Metadata.DeletionTimestamp != nil)
		pods = append(pods, p)
	}
	sort.SliceStable(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	return pods, nil
}

// podStatus resume o estado como o kubectl get pods: o motivo de um container
// parado (CrashLoopBackOff, ImagePullBackOff…) diz mais que a fase.
func podStatus(phase, reason string, inits, conts []containerStatus, deleting bool) string {
	if deleting {
		return "Terminating"
	}
	if reason != "" {
		return reason // Evicted, NodeLost…
	}
	for i, c := range inits {
		switch {
		case c.State.Terminated != nil && c.State.Terminated.ExitCode == 0:
			continue
		case c.State.Terminated != nil:
			return "Init:" + orDefault(c.State.Terminated.Reason, "Error")
		case c.State.Waiting != nil && c.State.Waiting.Reason != "" && c.State.Waiting.Reason != "PodInitializing":
			return "Init:" + c.State.Waiting.Reason
		default:
			return fmt.Sprintf("Init:%d/%d", i, len(inits))
		}
	}
	status := phase
	for _, c := range conts {
		switch {
		case c.State.Waiting != nil && c.State.Waiting.Reason != "":
			status = c.State.Waiting.Reason
		case c.State.Terminated != nil && c.State.Terminated.Reason != "":
			status = c.State.Terminated.Reason
		}
	}
	if status == "Succeeded" {
		status = "Completed"
	}
	return status
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Age é a idade curta, como o kubectl mostra: 45s, 12m, 3h, 8d.
func Age(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Describe devolve o kubectl describe do pod (só leitura).
func Describe(ctx context.Context, kctx, ns, pod string) (string, error) {
	out, err := run(ctx, append(base(kctx), "-n", ns, "describe", "pod", pod)...)
	return string(out), err
}

// ShellCmd abre um shell no pod: bash se tiver, senão sh.
func ShellCmd(kctx, ns, pod, container string) *exec.Cmd {
	args := []string{"--context", kctx, "-n", ns, "exec", "-it", pod}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh")
	return exec.Command(Bin, args...)
}

// LogsCmd acompanha os logs do pod (as últimas linhas e o que vier depois).
func LogsCmd(kctx, ns, pod, container string) *exec.Cmd {
	args := []string{"--context", kctx, "-n", ns, "logs", "-f", "--tail=2000", pod}
	if container != "" {
		args = append(args, "-c", container)
	}
	return exec.Command(Bin, args...)
}
