package dbconn

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSplit(t *testing.T) {
	text := "SELECT 'a;b', \"c;d\", `e;f` -- x;y\nFROM t;\n/* ; */ UPDATE t SET v = 'it''s;' WHERE id = 1;\n" +
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql;\n  \n"
	got := Split(text)
	if len(got) != 3 {
		for _, s := range got {
			t.Logf("%q", s.Text)
		}
		t.Fatalf("comandos = %d, quer 3", len(got))
	}
	if !strings.HasPrefix(got[1].Text, "/* ; */ UPDATE") || !strings.HasSuffix(got[2].Text, "LANGUAGE plpgsql") {
		t.Fatalf("cortou errado: %q | %q", got[1].Text, got[2].Text)
	}
	// só comentário não vira comando
	if len(Split("-- nada\n")) != 0 {
		t.Fatal("comentário sozinho virou comando")
	}
}

func TestAt(t *testing.T) {
	text := "SELECT 1;\nSELECT 2;\n\n"
	for _, c := range []struct {
		pos  int
		want string
	}{{0, "SELECT 1"}, {5, "SELECT 1"}, {9, "SELECT 1"}, {12, "SELECT 2"}, {len(text), "SELECT 2"}} {
		s, ok := At(text, c.pos)
		if !ok || s.Text != c.want {
			t.Errorf("At(%d) = %q, quer %q", c.pos, s.Text, c.want)
		}
	}
}

func TestReturnsRows(t *testing.T) {
	for stmt, want := range map[string]bool{
		"select 1": true, "  /* oi */ SHOW TABLES": true, "WITH x AS (SELECT 1) SELECT * FROM x": true,
		"(select 1)": true, "insert into t values (1)": false, "INSERT INTO t VALUES (1) RETURNING id": true,
		"update t set a=1": false, "USE loja": false,
	} {
		if returnsRows(stmt) != want {
			t.Errorf("returnsRows(%q) != %v", stmt, want)
		}
	}
}

// TestBancos roda contra bancos de verdade (MTSH_DB_TEST=1, ver docker run no README de dev):
// mariadb em :13306, mysql em :13307, postgres em :15432, senha s3nha, base loja.
func TestBancos(t *testing.T) {
	if os.Getenv("MTSH_DB_TEST") == "" {
		t.Skip("MTSH_DB_TEST não definido")
	}
	for _, c := range []struct{ name, engine, addr, user string }{
		{"mariadb", MySQL, "127.0.0.1:13306", "root"},
		{"mysql", MySQL, "127.0.0.1:13307", "root"},
		{"postgres", Postgres, "127.0.0.1:15432", "postgres"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			db, err := Open(ctx, c.engine, c.addr, c.user, "s3nha", "loja")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			must := func(stmt string) Result {
				t.Helper()
				r, err := Run(ctx, db, stmt)
				if err != nil {
					t.Fatalf("%s: %v", stmt, err)
				}
				return r
			}
			must("DROP TABLE IF EXISTS clientes")
			must("CREATE TABLE clientes (id INT PRIMARY KEY, nome VARCHAR(50), nasc DATE, obs TEXT)")
			if r := must("INSERT INTO clientes VALUES (1, 'Ana', '1990-05-01', NULL), (2, 'Bia', NULL, 'oi')"); r.Affected != 2 || r.IsQuery {
				t.Fatalf("insert: %+v", r)
			}
			r := must("SELECT id, nome, nasc, obs FROM clientes ORDER BY id")
			if !r.IsQuery || len(r.Rows) != 2 || strings.Join(r.Columns, ",") != "id,nome,nasc,obs" {
				t.Fatalf("select: %+v", r)
			}
			if r.Rows[0][1].Text != "Ana" || r.Rows[0][2].Text != "1990-05-01" || !r.Rows[0][3].Null || r.Rows[1][3].Text != "oi" {
				t.Fatalf("valores: %+v", r.Rows)
			}
			// transação na mesma conexão: o ROLLBACK desfaz
			must("BEGIN")
			must("DELETE FROM clientes")
			must("ROLLBACK")
			if r := must("SELECT count(*) FROM clientes"); r.Rows[0][0].Text != "2" {
				t.Fatalf("rollback não valeu: %v", r.Rows)
			}
			// limite de linhas
			if c.engine == Postgres {
				r = must("SELECT g FROM generate_series(1, 1500) g")
			} else {
				Run(ctx, db, "SET SESSION cte_max_recursion_depth = 2000") // MySQL 8 para em 1000; MariaDB não tem
				r = must("WITH RECURSIVE s(g) AS (SELECT 1 UNION ALL SELECT g+1 FROM s WHERE g < 1500) SELECT g FROM s")
			}
			if len(r.Rows) != MaxRows || !r.Truncated {
				t.Fatalf("limite: %d linhas, truncado=%v", len(r.Rows), r.Truncated)
			}
			// erro de SQL volta como erro, sem derrubar a conexão
			if _, err := Run(ctx, db, "SELECT * FROM nao_existe"); err == nil {
				t.Fatal("tabela inexistente devia dar erro")
			}
			cur, items, err := Objects(ctx, db, c.engine, false)
			if err != nil || cur != "loja" {
				t.Fatalf("objects: cur=%q err=%v", cur, err)
			}
			found := false
			for _, it := range items {
				if it.Name == "clientes" {
					found = true
					if r := must(it.Query); len(r.Rows) != 2 {
						t.Fatalf("abrir tabela: %+v", r)
					}
				}
			}
			if !found {
				t.Fatalf("clientes não apareceu: %+v", items)
			}
			// cancelar um comando demorado
			slow := "SELECT SLEEP(10)"
			if c.engine == Postgres {
				slow = "SELECT pg_sleep(10)"
			}
			cctx, ccancel := context.WithTimeout(ctx, 500*time.Millisecond)
			start := time.Now()
			Run(cctx, db, slow)
			ccancel()
			if time.Since(start) > 5*time.Second {
				t.Fatalf("cancelar demorou %v", time.Since(start))
			}
			if r := must("SELECT 1"); r.Rows[0][0].Text != "1" {
				t.Fatal("conexão não voltou depois de cancelar")
			}
			// usuário que exige TLS (REQUIRE SSL): o cliente mysql entra, o mtsh também tem que entrar
			if c.engine == MySQL {
				must("DROP USER IF EXISTS 'seguro'@'%'")
				must("CREATE USER 'seguro'@'%' IDENTIFIED BY 'Abc123!' REQUIRE SSL")
				sdb, err := Open(ctx, c.engine, c.addr, "seguro", "Abc123!", "")
				if err != nil {
					t.Fatalf("usuário com REQUIRE SSL: %v", err)
				}
				sdb.Close()
			}
		})
	}
	// MySQL sem base: lista as bases, e USE entra numa
	ctx := context.Background()
	db, err := Open(ctx, MySQL, "127.0.0.1:13306", "root", "s3nha", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cur, items, err := Objects(ctx, db, MySQL, false)
	if err != nil || cur != "" {
		t.Fatalf("sem base: cur=%q err=%v", cur, err)
	}
	for _, it := range items {
		if it.Name == "loja" && it.IsDB {
			if _, err := Run(ctx, db, it.Query); err != nil {
				t.Fatal(err)
			}
		}
	}
	if cur, _, _ = Objects(ctx, db, MySQL, false); cur != "loja" {
		t.Fatalf("USE não valeu: %q", cur)
	}
}
