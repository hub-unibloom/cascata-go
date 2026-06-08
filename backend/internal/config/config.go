package config

import (
	"log"
	"os"
	"path/filepath"
)

var (
	StorageRoot       string
	MigrationsRoot    string
	TempUploadRoot     string
	NginxDynamicRoot   string
	SystemDatabaseURL  string
	SystemDatabaseName string
	SystemJWTSecret    string
	InternalCtrlSecret string
	CryptoEngineURL    string
	OtpEnabled         bool
	OtpSecret          string

	// Magic numbers for file validation
	MagicNumbers = map[string][]string{
		"jpg": {"FFD8FF"},
		"png": {"89504E47"},
		"gif": {"47494638"},
		"pdf": {"25504446"},
		"exe": {"4D5A"},
		"zip": {"504B0304"},
		"rar": {"52617221"},
		"mp3": {"494433", "FFF3", "FFF2"},
		"mp4": {"000000", "66747970"},
	}
)

func InitConfig() {
	appRoot, err := filepath.Abs(".")
	if err != nil {
		log.Fatalf("[Config] Failed to resolve APP_ROOT: %v", err)
	}

	StorageRoot = getEnv("STORAGE_ROOT", filepath.Join(appRoot, "../storage"))
	MigrationsRoot = getEnv("MIGRATIONS_ROOT", filepath.Join(appRoot, "migrations"))
	TempUploadRoot = getEnv("TEMP_UPLOAD_ROOT", filepath.Join(appRoot, "temp_uploads"))
	NginxDynamicRoot = getEnv("NGINX_DYNAMIC_ROOT", "/etc/nginx/conf.d/dynamic")

	ensureDir(StorageRoot)
	ensureDir(NginxDynamicRoot)
	ensureDir(TempUploadRoot)

	SystemDatabaseURL = os.Getenv("SYSTEM_DATABASE_URL")
	if SystemDatabaseURL == "" {
		log.Fatal("[Config] FATAL: SYSTEM_DATABASE_URL is not defined.")
	}

	SystemDatabaseName = getEnv("DB_NAME", "cascata_system")

	SystemJWTSecret = os.Getenv("SYSTEM_JWT_SECRET")
	if SystemJWTSecret == "" {
		log.Fatal("[Config] FATAL: SYSTEM_JWT_SECRET is missing. Security cannot be guaranteed.")
	}

	InternalCtrlSecret = getEnv("INTERNAL_CTRL_SECRET", "fallback-danger-internal-secret")
	CryptoEngineURL = getEnv("CRYPTO_ENGINE_URL", "http://crypto_engine:3000")
	OtpEnabled = os.Getenv("CASCATA_OTP_ENABLED") == "true"
	OtpSecret = os.Getenv("CASCATA_OTP_SECRET")
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func ensureDir(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			log.Printf("[Config] Error creating directory %s: %v", dir, err)
		}
	}
}

// GetMaxUploadSize returns the limit for standard uploads
func GetMaxUploadSize() int64 {
	return 100 * 1024 * 1024 // 100MB
}

// GetMaxBackupSize returns the limit for backup uploads
func GetMaxBackupSize() int64 {
	return 5 * 1024 * 1024 * 1024 // 5GB
}
