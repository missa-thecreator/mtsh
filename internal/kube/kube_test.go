package kube

import (
	"strings"
	"testing"
	"time"
)

func TestParseContexts(t *testing.T) {
	data := []byte(`{
	  "current-context": "lab",
	  "clusters": [{"name": "c-lab", "cluster": {"server": "https://10.1.2.3:6443", "certificate-authority-data": "DATA+OMITTED"}}],
	  "contexts": [
	    {"name": "lab", "context": {"cluster": "c-lab", "user": "admin", "namespace": "web"}},
	    {"name": "aws", "context": {"cluster": "c-aws", "user": "eu"}}
	  ]}`)
	cs, err := parseContexts(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Name != "aws" || cs[1].Name != "lab" {
		t.Fatalf("ordem/qtde: %+v", cs)
	}
	lab := cs[1]
	if !lab.Current || lab.Server != "https://10.1.2.3:6443" || lab.Namespace != "web" || lab.User != "admin" {
		t.Fatalf("lab: %+v", lab)
	}
	if cs[0].Current || cs[0].Server != "" {
		t.Fatalf("aws: %+v", cs[0])
	}
}

func TestParsePods(t *testing.T) {
	data := []byte(`{"items": [
	  {"metadata": {"name": "web-1", "namespace": "app", "creationTimestamp": "2026-01-01T10:00:00Z"},
	   "spec": {"nodeName": "n1", "containers": [{"name": "web"}, {"name": "proxy"}]},
	   "status": {"phase": "Running", "containerStatuses": [
	     {"name": "web", "ready": true, "restartCount": 2, "state": {"running": {}}},
	     {"name": "proxy", "ready": false, "restartCount": 5, "state": {"waiting": {"reason": "CrashLoopBackOff"}}}]}},
	  {"metadata": {"name": "job-x", "namespace": "app", "creationTimestamp": "2026-01-01T10:00:00Z"},
	   "spec": {"containers": [{"name": "job"}]},
	   "status": {"phase": "Succeeded", "containerStatuses": [
	     {"name": "job", "ready": false, "state": {"terminated": {"reason": "Completed", "exitCode": 0}}}]}},
	  {"metadata": {"name": "api-0", "namespace": "app", "creationTimestamp": "2026-01-01T10:00:00Z"},
	   "spec": {"containers": [{"name": "api"}]},
	   "status": {"phase": "Pending",
	     "initContainerStatuses": [{"name": "migra", "state": {"waiting": {"reason": "PodInitializing"}}}],
	     "containerStatuses": [{"name": "api", "state": {"waiting": {"reason": "PodInitializing"}}}]}},
	  {"metadata": {"name": "old", "namespace": "a", "creationTimestamp": "2026-01-01T10:00:00Z", "deletionTimestamp": "2026-01-02T10:00:00Z"},
	   "spec": {"containers": [{"name": "c"}]}, "status": {"phase": "Running"}},
	  {"metadata": {"name": "ok", "namespace": "a", "creationTimestamp": "2026-01-01T10:00:00Z"},
	   "spec": {"containers": [{"name": "c"}]},
	   "status": {"phase": "Running", "containerStatuses": [{"name": "c", "ready": true, "state": {"running": {}}}]}}
	]}`)
	pods, err := parsePods(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, ready, status string
		restarts            int
		healthy             bool
	}{
		{"ok", "1/1", "Running", 0, true}, // namespace "a" vem antes de "app"
		{"old", "0/1", "Terminating", 0, false},
		{"api-0", "0/1", "Init:0/1", 0, false},
		{"job-x", "0/1", "Completed", 0, true},
		{"web-1", "1/2", "CrashLoopBackOff", 7, false},
	}
	if len(pods) != len(want) {
		t.Fatalf("%d pods", len(pods))
	}
	for i, w := range want {
		p := pods[i]
		if p.Name != w.name || p.Ready != w.ready || p.Status != w.status || p.Restarts != w.restarts || p.Healthy() != w.healthy {
			t.Errorf("pod %d = %+v (healthy %v), quero %+v", i, p, p.Healthy(), w)
		}
	}
	if c := pods[4].Containers; len(c) != 2 || c[0] != "web" || pods[4].Node != "n1" {
		t.Errorf("containers/nó: %+v", pods[4])
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 47 * time.Hour: "47h", 72 * time.Hour: "3d",
	} {
		if got := Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v) = %s, quero %s", d, got, want)
		}
	}
}

func TestCmds(t *testing.T) {
	sh := strings.Join(ShellCmd("lab", "app", "web-1", "web").Args[1:], " ")
	if !strings.HasPrefix(sh, "--context lab -n app exec -it web-1 -c web -- sh -c ") {
		t.Fatalf("shell: %q", sh)
	}
	lg := LogsCmd("lab", "app", "web-1", "").Args
	for _, a := range lg {
		if a == "-c" {
			t.Fatalf("sem container não devia ter -c: %q", lg)
		}
	}
}
