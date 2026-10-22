package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"

	"github.com/nkanaev/yarr/src/rsshub"
)

const defaultReadItemsKeepLimit = 1000

func readItemsKeepLimit(value interface{}) (int64, bool) {
	var number float64
	switch value := value.(type) {
	case int:
		number = float64(value)
	case int64:
		number = float64(value)
	case float64:
		number = value
	default:
		return 0, false
	}
	if number >= 1 && number <= 9007199254740991 && math.Trunc(number) == number {
		return int64(number), true
	}
	return 0, false
}

func settingsDefaults() map[string]interface{} {
	return map[string]interface{}{
		"filter":                "",
		"feed_list_width":       0,
		"feed_sort":             "time",
		"item_list_width":       0,
		"refresh_rate":          0,
		"rsshub_base_url":       "",
		"backup_enabled":        false,
		"auto_read_scroll":      false,
		"unread_first":          true,
		"sort_newest_first":     true,
		"read_items_keep_limit": defaultReadItemsKeepLimit,
	}
}

func (s *Storage) GetSettingsValue(key string) interface{} {
	if _, ok := settingsDefaults()[key]; !ok {
		return nil
	}
	row := s.db.QueryRow(`select val from settings where key=?`, key)
	var val []byte
	if err := row.Scan(&val); err != nil {
		if err != sql.ErrNoRows {
			log.Print(err)
		}
		return settingsDefaults()[key]
	}
	if len(val) == 0 {
		return nil
	}
	var valDecoded interface{}
	if err := json.Unmarshal([]byte(val), &valDecoded); err != nil {
		log.Print(err)
		return nil
	}
	return valDecoded
}

func (s *Storage) GetSettingsValueInt64(key string) int64 {
	val := s.GetSettingsValue(key)
	if key == "read_items_keep_limit" {
		if limit, ok := readItemsKeepLimit(val); ok {
			return limit
		}
		return defaultReadItemsKeepLimit
	}
	if val != nil {
		if fval, ok := val.(float64); ok {
			return int64(fval)
		}
	}
	return 0
}

func (s *Storage) GetSettingsValueString(key string) string {
	val := s.GetSettingsValue(key)
	if val != nil {
		if sval, ok := val.(string); ok {
			return sval
		}
	}
	return ""
}

func (s *Storage) GetSettingsValueBool(key string) bool {
	val := s.GetSettingsValue(key)
	if val != nil {
		if bval, ok := val.(bool); ok {
			return bval
		}
	}
	return false
}

func (s *Storage) GetSettings() map[string]interface{} {
	result := settingsDefaults()
	rows, err := s.db.Query(`select key, val from settings;`)
	if err != nil {
		log.Print(err)
		return result
	}
	for rows.Next() {
		var key string
		var val []byte
		var valDecoded interface{}

		rows.Scan(&key, &val)
		if _, ok := result[key]; !ok {
			continue
		}
		if err = json.Unmarshal([]byte(val), &valDecoded); err != nil {
			log.Print(err)
			continue
		}
		if key == "read_items_keep_limit" {
			limit, ok := readItemsKeepLimit(valDecoded)
			if !ok {
				continue
			}
			valDecoded = limit
		}
		result[key] = valDecoded
	}
	return result
}

func (s *Storage) UpdateSettings(kv map[string]interface{}) bool {
	if value, exists := kv["read_items_keep_limit"]; exists {
		if _, ok := readItemsKeepLimit(value); !ok {
			return false
		}
	}
	defaults := settingsDefaults()
	for key, val := range kv {
		if defaults[key] == nil {
			continue
		}
		if key == "rsshub_base_url" {
			sval, ok := val.(string)
			if !ok {
				return false
			}
			normalized, err := rsshub.NormalizeBaseList(sval)
			if err != nil {
				log.Print(err)
				return false
			}
			val = normalized
		}
		if key == "unread_first" || key == "sort_newest_first" {
			if _, ok := val.(bool); !ok {
				return false
			}
		}
		if key == "feed_sort" {
			sval, ok := val.(string)
			if !ok || (sval != "name" && sval != "time" && sval != "count") {
				return false
			}
		}
		valEncoded, err := json.Marshal(val)
		if err != nil {
			log.Print(err)
			return false
		}
		_, err = s.db.Exec(`
			insert into settings (key, val) values (?, ?)
			on conflict (key) do update set val=?`,
			key, valEncoded, valEncoded,
		)
		if err != nil {
			log.Print(err)
			return false
		}
	}
	return true
}

// BulkUpdateReadingSetting updates existing scopes without changing creation defaults.
func (s *Storage) BulkUpdateReadingSetting(folderID *int64, field string, value bool) error {
	if field != "unread_first" && field != "auto_read_scroll" {
		return fmt.Errorf("unsupported reading setting: %s", field)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	folderWhere, feedWhere := "", ""
	args := []interface{}{value}
	if folderID != nil {
		var id int64
		if err := tx.QueryRow(`select id from folders where id = ?`, *folderID).Scan(&id); err != nil {
			return err
		}
		folderWhere, feedWhere = " where id = ?", " where folder_id = ?"
		args = append(args, *folderID)
	}
	// field is restricted to the two columns above; values and IDs stay bound.
	if _, err := tx.Exec(`update folders set `+field+` = ?`+folderWhere, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`update feeds set `+field+` = ?`+feedWhere, args...); err != nil {
		return err
	}
	if folderID == nil {
		encoded, _ := json.Marshal(value)
		if _, err := tx.Exec(`insert into settings (key, val) values (?, ?)
			on conflict (key) do update set val = excluded.val`, field, encoded); err != nil {
			return err
		}
	}
	return tx.Commit()
}
