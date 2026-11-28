package dbconn

import (
	"testing"
	"time"
)

func TestExportText(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 1500000, time.UTC)
	for _, c := range []struct {
		v    any
		typ  string
		want string
	}{
		{when, "TIMESTAMP", "2026-03-04 05:06:07.0015"}, // a grade corta nos segundos; o arquivo não
		{when.Truncate(time.Second), "TIMESTAMP", "2026-03-04 05:06:07"},
		{when, "TIMESTAMPTZ", "2026-03-04 05:06:07.0015+00:00"},
		{when, "DATE", "2026-03-04"},
		{[]byte("Calçados"), "VARCHAR", "Calçados"},
		{int64(42), "INT4", "42"},
	} {
		if got := exportText(c.v, c.typ); got != c.want {
			t.Errorf("exportText(%#v, %s) = %q, quero %q", c.v, c.typ, got, c.want)
		}
	}
}

func TestTableOf(t *testing.T) {
	for stmt, want := range map[string]string{
		"SELECT * FROM produtos LIMIT 100;":                "produtos",
		`SELECT * FROM "vendas"."pedidos" LIMIT 100;`:      "vendas.pedidos",
		"select id\nfrom `clientes` c join pedidos p on 1": "clientes",
		"-- from comentario\nSELECT 1":                     "",
		"SELECT now()":                                     "",
		"SELECT g FROM generate_series(1, 10) g":           "",
	} {
		if got := TableOf(stmt); got != want {
			t.Errorf("TableOf(%q) = %q, quero %q", stmt, got, want)
		}
	}
}
