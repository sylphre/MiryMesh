// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"errors"
	"log/slog"
)

// usesMySQLSyntax is true for MySQL and MariaDB, which spell the upsert differently.
func (s *SetupStoreDB) usesMySQLSyntax() bool { return s.driver == "mysql" || s.driver == "mariadb" }

// GetSetting returns a value saved with SetSetting (table app_settings).
func (s *SetupStoreDB) GetSetting(key string) (string, bool) {
	var v string
	err := s.db.QueryRow(`SELECT setting_value FROM app_settings WHERE setting_key=`+s.ph(1), key).Scan(&v)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("setup: read setting failed", "key", key, "err", err)
		}
		return "", false
	}
	return v, true
}

// SetSetting inserts or replaces a value in app_settings.
func (s *SetupStoreDB) SetSetting(key, value string) error {
	q := `INSERT INTO app_settings (setting_key, setting_value) VALUES (` + s.ph(1) + `, ` + s.ph(2) + `) ON CONFLICT (setting_key) DO UPDATE SET setting_value = EXCLUDED.setting_value`
	if s.usesMySQLSyntax() {
		q = `INSERT INTO app_settings (setting_key, setting_value) VALUES (?, ?) ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`
	}
	_, err := s.db.Exec(q, key, value)
	return err
}
