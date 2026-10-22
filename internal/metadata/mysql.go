// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
)

// mysqlConfig parses dsn and pins the options the rest of the code relies on:
//
//   - parseTime=true: DATETIME columns must scan into time.Time (sessions, pair_tokens).
//   - loc=UTC: time.Time arguments are sent as UTC.
//   - time_zone='+00:00' (unless the DSN sets one): CURRENT_TIMESTAMP defaults are UTC too,
//     so server-generated and application-generated timestamps agree.
func mysqlConfig(dsn string) (*mysqldrv.Config, error) {
	cfg, err := mysqldrv.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if _, ok := cfg.Params["time_zone"]; !ok {
		if cfg.Params == nil {
			cfg.Params = make(map[string]string)
		}
		cfg.Params["time_zone"] = "'+00:00'"
	}
	return cfg, nil
}

// openMySQL opens a MySQL / MariaDB handle with the options from mysqlConfig applied.
func openMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysqlConfig(dsn)
	if err != nil {
		return nil, err
	}
	connector, err := mysqldrv.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("mysql connector: %w", err)
	}
	return sql.OpenDB(connector), nil
}

// mysqlMigration returns the statements to run for one migration on MySQL / MariaDB.
// migrations/mysql/<file> (hand written) takes precedence over the generic
// migrations/<file>, which is only converted mechanically by toMySQLDialect.
// The generic files cannot be converted safely: TEXT keys, inline REFERENCES and the
// reserved word "key" need real schema decisions.
func mysqlMigration(name string, generic []byte) ([]string, error) {
	text := toMySQLDialect(string(generic))
	override := path.Join(path.Dir(name), "mysql", path.Base(name))
	b, err := fs.ReadFile(migrationsFS, override)
	switch {
	case err == nil:
		text = string(b)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("read %s: %w", override, err)
	}
	return splitSQLStatements(text), nil
}

// splitSQLStatements splits a migration into single statements (the MySQL driver
// refuses multiple statements per Exec unless multiStatements=true, which we do not
// enable). It skips "--" line comments and ignores ';' inside quoted text.
func splitSQLStatements(src string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
			cur.WriteByte(c)
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			cur.WriteByte('\n')
		case c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
