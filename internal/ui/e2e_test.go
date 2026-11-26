package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mtsh/internal/dbconn"
)

// TestE2ETunel testa o caminho real: ssh mestre com socket de controle → túnel
// → banco, e o SFTP pelo mesmo socket. Precisa de um sshd em 127.0.0.1:12222
// (usuário dev, chave em MTSH_E2E_SSH_KEY) na mesma rede docker dos bancos
// mtsh-t-mariadb e mtsh-t-pg (senha s3nha, base loja).
func TestE2ETunel(t *testing.T) {
	key := os.Getenv("MTSH_E2E_SSH_KEY")
	if key == "" {
		t.Skip("MTSH_E2E_SSH_KEY não definido")
	}
	ctl := filepath.Join(t.TempDir(), "ctl")
	target := "dev@127.0.0.1"
	master := exec.Command("ssh", "-i", key, "-p", "12222", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes", "-o", "ControlMaster=yes", "-o", "ControlPath="+ctl, "-o", "ControlPersist=no", "-N", target)
	if err := master.Start(); err != nil {
		t.Fatal(err)
	}
	defer master.Process.Kill()
	if !waitCtl(ctl, func() bool { return false }) {
		t.Fatal("socket de controle não apareceu")
	}

	for _, c := range []struct{ engine, host, user string }{
		{dbconn.MySQL, "mtsh-t-mariadb", "root"},
		{dbconn.Postgres, "mtsh-t-pg", "postgres"},
	} {
		addr, closeT, err := openTunnel(ctl, target, c.host, dbconn.DefaultPort(c.engine))
		if err != nil {
			t.Fatalf("%s: %v", c.engine, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		db, err := dbconn.Open(ctx, c.engine, addr, c.user, "s3nha", "loja")
		if err != nil {
			t.Fatalf("%s pelo túnel: %v", c.engine, err)
		}
		r, err := dbconn.Run(ctx, db, "SELECT 40 + 2")
		if err != nil || r.Rows[0][0].Text != "42" {
			t.Fatalf("%s: %v %+v", c.engine, err, r)
		}
		t.Logf("%s pelo túnel %s: ok", c.engine, addr)
		db.Close()
		cancel()
		closeT()
	}

	// SFTP pelo mesmo socket (o comando que o mtsh usa de verdade)
	v := &sftpView{ctl: ctl, target: target}
	msg := sftpConnect(v, func() bool { return false })().(sftpReadyMsg)
	if msg.err != nil {
		t.Fatalf("sftp: %v", msg.err)
	}
	defer msg.client.Close()
	local := t.TempDir()
	os.MkdirAll(filepath.Join(local, "p", "sub"), 0o755)
	os.WriteFile(filepath.Join(local, "p", "sub", "f"), []byte("ola"), 0o640)
	if err := copyTree(localFS{}, filepath.Join(local, "p"), remoteFS{msg.client}, msg.home+"/p", &job{}, false); err != nil {
		t.Fatalf("upload de pasta: %v", err)
	}
	st, err := msg.client.Stat(msg.home + "/p/sub/f")
	if err != nil || st.Size() != 3 || st.Mode().Perm() != 0o640 {
		t.Fatalf("arquivo remoto: %v %v", st, err)
	}
	t.Logf("sftp em %s: upload de pasta ok", msg.home)

	// ssh em segundo plano (sem terminal, como o banco via servidor sem aba aberta)
	bg := filepath.Join(t.TempDir(), "bg")
	c := exec.Command("ssh", "-N", "-o", "BatchMode=yes", "-i", key, "-p", "12222", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "-o", "ControlMaster=yes", "-o", "ControlPath="+bg, "-o", "ControlPersist=no", target)
	if err := startMaster(c, bg); err != nil {
		t.Fatalf("ssh em segundo plano: %v", err)
	}
	defer c.Process.Kill()
	addr, closeT, err := openTunnel(bg, target, "mtsh-t-mariadb", 3306)
	if err != nil {
		t.Fatal(err)
	}
	defer closeT()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := dbconn.Open(ctx, dbconn.MySQL, addr, "root", "s3nha", "loja")
	if err != nil {
		t.Fatalf("banco pelo ssh em segundo plano: %v", err)
	}
	db.Close()
	t.Log("ssh em segundo plano + túnel: ok")
}
