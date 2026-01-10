package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalCORSOrigins is the fixed CORS allowlist of the desktop sidecar.
const LocalCORSOrigins = "tauri://localhost,http://tauri.localhost,http://localhost:3000,http://127.0.0.1:3000"

// LocalSecrets are the per-installation secrets of the desktop sidecar.
type LocalSecrets struct {
	JWTSecret         string `json:"jwtSecret"`
	DataEncryptionKey string `json:"dataEncryptionKey"`
}

// ApplyLocal switches c into the desktop-sidecar shape for dataDir (which must
// be an absolute path): production semantics, SQLite under the data dir,
// memory cache, local storage, loopback-only listener and a reduced surface.
func ApplyLocal(c *Config, dataDir string, secrets LocalSecrets) error {
	if strings.TrimSpace(dataDir) == "" || !filepath.IsAbs(dataDir) {
		return errors.New("--local requires --data-dir <absolute path>")
	}
	c.Local = LocalConfig{Enabled: true, DataDir: dataDir}
	c.App.Env = EnvProd
	c.Database.Driver = DriverSQLite
	c.Database.SQLite.Path = filepath.Join(dataDir, "riochat.db")
	c.Database.SQLite.DSN = ""
	c.Cache.Driver = CacheMemory
	c.Storage.Backend = StorageLocal
	c.Storage.Local.RootDir = filepath.Join(dataDir, "storage")
	c.GeoIP.Provider = "none"
	c.Server.TrustedProxies = ""
	c.Server.FrontendDistDir = ""
	c.Server.ListenAddr = "127.0.0.1:0"
	c.Server.CORSAllowOrigin = LocalCORSOrigins
	c.Admin.Username = "owner"
	c.Security.JWTSecret = secrets.JWTSecret
	c.Security.DataEncryptionKey = secrets.DataEncryptionKey
	return nil
}

// LoadOrCreateLocalSecrets reads <dataDir>/secrets.json, creating the data
// directory (0700) and the file (0600, atomically) on first use.
func LoadOrCreateLocalSecrets(dataDir string) (LocalSecrets, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return LocalSecrets{}, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "secrets.json")
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var s LocalSecrets
		if err := json.Unmarshal(raw, &s); err != nil {
			return LocalSecrets{}, fmt.Errorf("secrets file %s is corrupt: %w", path, err)
		}
		if len(s.JWTSecret) < 32 || len(s.DataEncryptionKey) < 32 {
			return LocalSecrets{}, fmt.Errorf("secrets file %s holds a secret shorter than 32 characters; delete the file to regenerate it (existing encrypted data will become unreadable)", path)
		}
		return s, nil
	case !errors.Is(err, os.ErrNotExist):
		return LocalSecrets{}, fmt.Errorf("read secrets file: %w", err)
	}

	s := LocalSecrets{JWTSecret: randomHex(32), DataEncryptionKey: randomHex(32)}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return LocalSecrets{}, err
	}
	tmp, err := os.CreateTemp(dataDir, "secrets-*.tmp")
	if err != nil {
		return LocalSecrets{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return LocalSecrets{}, err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return LocalSecrets{}, err
	}
	if err := tmp.Close(); err != nil {
		return LocalSecrets{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return LocalSecrets{}, err
	}
	return s, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("config: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
