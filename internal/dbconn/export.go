package dbconn

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// ExportCSV roda stmt de novo e grava todas as linhas em w como CSV, sem o
// limite de MaxRows: vai linha a linha, sem guardar o resultado na memória.
// UTF-8 com BOM (o Excel reconhece os acentos); NULL vira campo vazio.
// count, se não for nil, recebe o total gravado até agora (pra mostrar o andamento).
func ExportCSV(ctx context.Context, db *sql.DB, stmt string, w io.Writer, count *atomic.Int64) (int, error) {
	if !returnsRows(stmt) {
		return 0, fmt.Errorf("só dá pra exportar consultas que devolvem linhas")
	}
	rows, err := db.QueryContext(ctx, stmt)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	typeNames := make([]string, len(cols))
	if types, err := rows.ColumnTypes(); err == nil {
		for i := range types {
			typeNames[i] = strings.ToUpper(types[i].DatabaseTypeName())
		}
	}

	bw := bufio.NewWriterSize(w, 64*1024)
	bw.WriteString("\xEF\xBB\xBF") // BOM
	cw := csv.NewWriter(bw)
	if err := cw.Write(cols); err != nil {
		return 0, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	rec := make([]string, len(cols))
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i, v := range vals {
			rec[i] = ""
			if v != nil {
				rec[i] = exportText(v, typeNames[i])
			}
		}
		if err := cw.Write(rec); err != nil {
			return n, err
		}
		n++
		if count != nil {
			count.Store(int64(n))
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, err
	}
	return n, bw.Flush()
}

// exportText é o valor como texto, sem perder nada: a grade mostra as datas
// até os segundos, mas o arquivo leva os microssegundos e o fuso.
func exportText(v any, typ string) string {
	if x, ok := v.(time.Time); ok {
		switch typ {
		case "DATE":
			return x.Format("2006-01-02")
		case "TIMESTAMPTZ":
			return x.Format("2006-01-02 15:04:05.999999-07:00")
		}
		return x.Format("2006-01-02 15:04:05.999999")
	}
	return format(v).Text
}

var fromRe = regexp.MustCompile("(?is)\\bfrom\\s+((?:[`\"]?[\\w$]+[`\"]?\\.)?[`\"]?[\\w$]+[`\"]?)")

// TableOf tenta achar a tabela de uma consulta (o primeiro FROM), pra sugerir
// o nome do arquivo. "" se não achar.
func TableOf(stmt string) string {
	src := stripComments(stmt)
	m := fromRe.FindStringSubmatchIndex(src)
	if m == nil || strings.HasPrefix(strings.TrimSpace(src[m[1]:]), "(") {
		return "" // FROM generate_series(…): é função, não tabela
	}
	return strings.NewReplacer("`", "", `"`, "").Replace(src[m[2]:m[3]])
}
