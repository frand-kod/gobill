package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"log/slog"
	"strings"

	"github.com/frand-kod/gobill/internal/secret"
)

// SettingsKey encrypts the SecretSettings values (set once at startup, same key as the NAS secrets).
// While nil, values are stored and read as they are.
var SettingsKey []byte

// SecretSettings are the settings stored encrypted.
var SecretSettings = map[string]bool{"telegram_bot": true, "smtp_pass": true, "webhook_secret": true, "alt_wga_password": true, "tripay_api_key": true, "tripay_private_key": true, "duitku_api_key": true, "metrics_token": true, "wg_private": true}

const encPrefix = "enc1:"

// SealSetting encrypts v for a secret key; anything else, an empty value or no key passes through.
func SealSetting(name, v string) string { return SealSettingWith(SettingsKey, name, v) }

// SealSettingWith is SealSetting with an explicit key (the importer passes its own).
func SealSettingWith(key []byte, name, v string) string {
	if !SecretSettings[name] || v == "" || key == nil || strings.HasPrefix(v, encPrefix) {
		return v
	}
	b, err := secret.Seal(key, []byte(v))
	if err != nil {
		return v
	}
	return encPrefix + base64.StdEncoding.EncodeToString(b)
}

// OpenSetting decrypts a value from SealSetting. A legacy plaintext value is returned as it is;
// an encrypted one that cannot be opened (wrong key) reads as empty.
func OpenSetting(v string) string { return OpenSettingWith(SettingsKey, v) }

// OpenSettingWith is OpenSetting with an explicit key (the restore check passes its own).
func OpenSettingWith(key []byte, v string) string {
	rest, ok := strings.CutPrefix(v, encPrefix)
	if !ok {
		return v
	}
	b, err := base64.StdEncoding.DecodeString(rest)
	if err == nil && key != nil {
		if b, err = secret.Open(key, b); err == nil {
			return string(b)
		}
	}
	return ""
}

type UpsertSettingParams struct {
	Key   string
	Value string
}

// ListSettings returns all settings with the secret ones decrypted.
func (q *Queries) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := q.ListSettingsRaw(ctx)
	for i := range rows {
		if SecretSettings[rows[i].Key] {
			rows[i].Value = OpenSetting(rows[i].Value)
		}
	}
	return rows, err
}

// UpsertSetting stores a setting, encrypting the secret ones.
func (q *Queries) UpsertSetting(ctx context.Context, arg UpsertSettingParams) error {
	return q.UpsertSettingRaw(ctx, UpsertSettingRawParams{Key: arg.Key, Value: SealSetting(arg.Key, arg.Value)})
}

// EncryptSettings encrypts legacy plaintext secret settings. Safe to run on every start.
func EncryptSettings(ctx context.Context, conn *sql.DB) error {
	q := New(conn)
	rows, err := q.ListSettingsRaw(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Value, encPrefix) && OpenSetting(r.Value) == "" {
			slog.Warn("setting cannot be decrypted with this key; it reads as empty until re-entered", "key", r.Key)
		}
		if sealed := SealSetting(r.Key, r.Value); sealed != r.Value {
			if err := q.UpsertSettingRaw(ctx, UpsertSettingRawParams{Key: r.Key, Value: sealed}); err != nil {
				return err
			}
		}
	}
	return nil
}
