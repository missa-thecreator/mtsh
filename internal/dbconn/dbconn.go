// Package dbconn conversa com MySQL/MariaDB e PostgreSQL: abre a conexão,
// roda um comando e devolve o resultado como texto, pronto pra desenhar.
package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	MySQL    = "mysql" // vale pro MariaDB também (mesmo protocolo)
	Postgres = "postgres"
)

// MaxRows é quanto de um SELECT fica na memória pra mostrar.
const MaxRows = 1000

func DefaultPort(engine string) int {
	if engine == Postgres {
		return 5432
	}
	return 3306
}

// Open conecta em addr (host:porta). Uma conexão só, de propósito: USE, SET e
// transações valem pros comandos seguintes, como num cliente de linha de comando.
func Open(ctx context.Context, engine, addr, user, pass, database string) (*sql.DB, error) {
	var db *sql.DB
	switch engine {
	case MySQL:
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = user, pass, "tcp", addr, database
		cfg.Timeout = 15 * time.Second
		cfg.AllowNativePasswords = true
		// TLS quando o servidor oferece, como o cliente mysql (--ssl-mode=PREFERRED):
		// usuário criado com REQUIRE SSL recusa conexão sem TLS com "Access denied"
		cfg.TLSConfig = "preferred"
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		db = sql.OpenDB(conn)
	case Postgres:
		if database == "" {
			database = "postgres"
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(user, pass), Host: addr, Path: "/" + database,
			RawQuery: "connect_timeout=15&sslmode=prefer"}
		cc, err := pgx.ParseConfig(u.String())
		if err != nil {
			return nil, err
		}
		db = stdlib.OpenDB(*cc)
	default:
		return nil, fmt.Errorf("tipo de banco desconhecido: %q", engine)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Result é o que um comando devolveu.
type Result struct {
	Columns   []string
	Rows      [][]Cell
	IsQuery   bool  // devolveu linhas (SELECT, SHOW…); senão vale Affected
	Affected  int64 // linhas afetadas (INSERT, UPDATE…)
	Truncated bool  // tinha mais que MaxRows
	Took      time.Duration
}

// Cell é um valor já como texto; Null diferencia NULL de texto vazio.
type Cell struct {
	Text string
	Null bool
}

// queryWords são os comandos que devolvem linhas.
var queryWords = map[string]bool{
	"select": true, "show": true, "with": true, "describe": true, "desc": true, "explain": true,
	"values": true, "table": true, "pragma": true, "help": true, "call": true,
}

// returnsRows decide se o comando devolve linhas (Query) ou só o total afetado (Exec).
func returnsRows(stmt string) bool {
	s := strings.ToLower(stripComments(stmt))
	first := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '(' })
	if len(first) == 0 {
		return false
	}
	return queryWords[first[0]] || strings.Contains(s, "returning")
}

// Run roda um comando. Com ctx cancelado, o banco recebe o pedido pra parar.
func Run(ctx context.Context, db *sql.DB, stmt string) (Result, error) {
	start := time.Now()
	var res Result
	if !returnsRows(stmt) {
		r, err := db.ExecContext(ctx, stmt)
		if err != nil {
			return res, err
		}
		res.Affected, _ = r.RowsAffected()
		res.Took = time.Since(start)
		return res, nil
	}
	rows, err := db.QueryContext(ctx, stmt)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	res.IsQuery = true
	if res.Columns, err = rows.Columns(); err != nil {
		return res, err
	}
	vals := make([]any, len(res.Columns))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(res.Rows) == MaxRows {
			res.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return res, err
		}
		row := make([]Cell, len(vals))
		for i, v := range vals {
			row[i] = format(v)
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil && !res.Truncated {
		return res, err
	}
	res.Took = time.Since(start)
	return res, nil
}

func format(v any) Cell {
	switch x := v.(type) {
	case nil:
		return Cell{Text: "NULL", Null: true}
	case []byte:
		if utf8.Valid(x) {
			return Cell{Text: string(x)}
		}
		return Cell{Text: fmt.Sprintf("0x%x", x)}
	case string:
		return Cell{Text: x}
	case time.Time:
		if x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0 {
			return Cell{Text: x.Format("2006-01-02")}
		}
		return Cell{Text: x.Format("2006-01-02 15:04:05")}
	case [16]byte: // uuid do postgres
		return Cell{Text: fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])}
	}
	return Cell{Text: fmt.Sprint(v)}
}

// Item é uma tabela (ou, no MySQL sem base escolhida, uma base).
type Item struct {
	Name  string // como mostrar
	Query string // o SELECT que abre a tabela, ou o USE da base
	IsDB  bool
}

// Objects lista as tabelas e views. Sem base escolhida (MySQL) ou com listDBs,
// lista as bases. No Postgres, trocar de base exige outra conexão: quem chama
// reconecta na base escolhida (Item.IsDB com Query vazia).
func Objects(ctx context.Context, db *sql.DB, engine string, listDBs bool) (current string, items []Item, err error) {
	if engine == Postgres {
		db.QueryRowContext(ctx, "SELECT current_database()").Scan(&current)
		if listDBs {
			rows, err := db.QueryContext(ctx, `SELECT datname FROM pg_database
				WHERE NOT datistemplate AND datallowconn ORDER BY datname`)
			if err != nil {
				return current, nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					return current, nil, err
				}
				items = append(items, Item{Name: name, IsDB: true})
			}
			return current, items, rows.Err()
		}
		rows, err := db.QueryContext(ctx, `SELECT table_schema, table_name FROM information_schema.tables
			WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema <> 'public', 1, 2`)
		if err != nil {
			return current, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var schema, name string
			if err := rows.Scan(&schema, &name); err != nil {
				return current, nil, err
			}
			label, q := name, pgIdent(name)
			if schema != "public" {
				label, q = schema+"."+name, pgIdent(schema)+"."+pgIdent(name)
			}
			items = append(items, Item{Name: label, Query: "SELECT * FROM " + q + " LIMIT 100;"})
		}
		return current, items, rows.Err()
	}
	var cur sql.NullString
	db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&cur)
	current = cur.String
	tables := cur.Valid && !listDBs
	q := "SHOW TABLES"
	if !tables {
		q = "SHOW DATABASES"
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return current, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return current, nil, err
		}
		if tables {
			items = append(items, Item{Name: name, Query: "SELECT * FROM " + myIdent(name) + " LIMIT 100;"})
		} else {
			items = append(items, Item{Name: name, Query: "USE " + myIdent(name), IsDB: true})
		}
	}
	return current, items, rows.Err()
}

func myIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
