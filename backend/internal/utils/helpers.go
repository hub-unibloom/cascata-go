package utils

import (
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cascata-backend/internal/config"
)

// --- SSRF SECURITY UTILS ---

// IsPrivateIP checks if an IP address is private or loopback
func IsPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}

	// Manual checks for private IPv4 ranges (Standard Go doesn't have isPrivate for all ranges in older versions)
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8
		if ip4[0] == 10 {
			return true
		}
		// 172.16.0.0/12
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return true
		}
		// 192.168.0.0/16
		if ip4[0] == 192 && ip4[1] == 168 {
			return true
		}
		// 0.0.0.0/8
		if ip4[0] == 0 {
			return true
		}
	}

	// IPv6 Private Check
	if ip6 := ip.To16(); ip6 != nil && ip.To4() == nil {
		// fc00::/7
		if ip6[0] == 0xfc || ip6[0] == 0xfd {
			return true
		}
	}

	return false
}

// ValidateTargetURL prevents SSRF by validating the hostname and resolved IPs
func ValidateTargetURL(targetURL string) (string, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("SSRF Protection: invalid URL: %v", err)
	}

	hostname := u.Hostname()
	if hostname == "localhost" || hostname == "::1" || hostname == "0.0.0.0" {
		return "", fmt.Errorf("SSRF Protection: localhost access denied")
	}

	internalServices := map[string]bool{
		"dragonfly":        true,
		"db":               true,
		"backend_control":  true,
		"backend_data":     true,
		"nginx":           true,
		"nginx_controller": true,
		"backend_engine":   true,
	}

	if internalServices[hostname] {
		return "", fmt.Errorf("SSRF Protection: Internal service access denied")
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return "", fmt.Errorf("SSRF Protection: DNS Resolution failed for %s", hostname)
	}

	for _, ip := range ips {
		if IsPrivateIP(ip.String()) {
			return "", fmt.Errorf("Security Violation: Host %s resolves to private IP %s. Request blocked.", hostname, ip.String())
		}
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("SSRF Protection: No IPs found for %s", hostname)
	}

	return ips[0].String(), nil
}

// --- FILESYSTEM UTILS ---

// GetSectorForExt maps file extensions to their respective sectors
func GetSectorForExt(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	sectors := map[string][]string{
		"visual":     {"jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", "tiff", "avif", "heic", "heif"},
		"motion":     {"mp4", "mov", "avi", "mkv", "webm", "flv", "wmv", "m4v", "mpg", "mpeg", "3gp"},
		"audio":      {"mp3", "wav", "flac", "aac", "ogg", "m4a", "wma", "m4p", "amr", "mid", "midi", "opus"},
		"docs":       {"pdf", "doc", "docx", "odt", "rtf", "txt", "pages", "epub", "mobi", "azw3"},
		"structured": {"csv", "json", "xml", "yaml", "yml", "sql", "xls", "xlsx", "ods", "tsv", "parquet", "avro"},
		"archives":   {"zip", "rar", "7z", "tar", "gz", "bz2", "iso", "dmg", "pkg", "xz", "zst"},
		"exec":       {"exe", "msi", "bin", "app", "deb", "rpm", "sh", "bat", "cmd", "vbs", "ps1"},
		"scripts":    {"js", "ts", "py", "rb", "php", "go", "rs", "c", "cpp", "h", "java", "cs", "swift", "kt"},
		"config":     {"env", "config", "ini", "xml", "manifest", "lock", "gitignore", "editorconfig", "toml"},
		"telemetry":  {"log", "dump", "out", "err", "crash", "report", "audit"},
		"messaging":  {"eml", "msg", "vcf", "chat", "ics", "pbx"},
		"ui_assets":  {"ttf", "otf", "woff", "woff2", "eot", "sketch", "fig", "ai", "psd", "xd"},
		"simulation": {"obj", "stl", "fbx", "dwg", "dxf", "dae", "blend", "step", "iges", "glf", "gltf", "glb"},
		"backup_sys": {"bak", "sql", "snapshot", "dump", "db", "sqlite", "sqlite3", "rdb"},
	}

	for sector, exts := range sectors {
		for _, e := range exts {
			if e == ext {
				return sector
			}
		}
	}
	return "global"
}

// CleanTempUploads removes files from TEMP_UPLOAD_ROOT older than 1 hour
func CleanTempUploads() {
	files, err := os.ReadDir(config.TempUploadRoot)
	if err != nil {
		return
	}

	now := time.Now()
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}

		if now.Sub(info.ModTime()) > time.Hour {
			path := filepath.Join(config.TempUploadRoot, file.Name())
			os.RemoveAll(path)
		}
	}
}

// ValidateMagicBytes validates the magic bytes of a file against a given extension
func ValidateMagicBytes(filePath, ext string) (bool, error) {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	
	// Fast-block known dangerous extensions
	dangerous := map[string]bool{
		"exe": true, "sh": true, "php": true, "pl": true, "py": true, 
		"rb": true, "bat": true, "cmd": true, "msi": true, "vbs": true,
	}
	if dangerous[ext] {
		return false, nil
	}

	sigs, ok := config.MagicNumbers[ext]
	if !ok {
		return true, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	buffer := make([]byte, 4)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return false, err
	}

	if n < 4 {
		// Too small to have signature, but if the sig exists we might still be able to check
	}

	hexSig := strings.ToUpper(fmt.Sprintf("%X", buffer[:n]))
	for _, sig := range sigs {
		if strings.HasPrefix(hexSig, sig) || strings.HasPrefix(sig, hexSig) {
			return true, nil
		}
	}

	return false, nil
}

// --- FORMAT VALIDATION UTILS ---

type FormatPreset struct {
	Label       string
	Regex       string
	Example     string
	Description string
}

var FormatPresets = map[string]FormatPreset{
	"email":       {Label: "Email", Regex: `^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`, Example: "user@example.com", Description: "Endereço de e-mail válido"},
	"cpf":         {Label: "CPF", Regex: `^\d{3}\.\d{3}\.\d{3}-\d{2}$`, Example: "123.456.789-00", Description: "CPF no formato XXX.XXX.XXX-XX"},
	"cnpj":        {Label: "CNPJ", Regex: `^\d{2}\.\d{3}\.\d{3}\/\d{4}-\d{2}$`, Example: "12.345.678/0001-99", Description: "CNPJ no formato XX.XXX.XXX/XXXX-XX"},
	"phone_br":    {Label: "Phone (BR)", Regex: `^\+?55\s?\(?\d{2}\)?\s?\d{4,5}-?\d{4}$`, Example: "+55 (11) 99999-1234", Description: "Telefone brasileiro com DDD"},
	"cep":         {Label: "CEP", Regex: `^\d{5}-?\d{3}$`, Example: "01310-100", Description: "CEP brasileiro"},
	"url":         {Label: "URL", Regex: `^https?:\/\/[a-zA-Z0-9\-]+(\.[a-zA-Z0-9\-]+)+(\/.*)?$`, Example: "https://example.com", Description: "URL com http ou https"},
	"uuid_format": {Label: "UUID", Regex: `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, Example: "a1b2c3d4-e5f6-7890-abcd-ef1234567890", Description: "UUID v4 padrão"},
	"date_br":     {Label: "Date (BR)", Regex: `^\d{2}\/\d{2}\/\d{4}$`, Example: "25/02/2026", Description: "Data no formato DD/MM/AAAA"},
}

// ValidateFormatPattern validates a value against a preset or custom regex
func ValidateFormatPattern(value, pattern string) (bool, error) {
	if value == "" || pattern == "" {
		return true, nil
	}

	resolvedPattern := pattern
	if preset, ok := FormatPresets[pattern]; ok {
		resolvedPattern = preset.Regex
	}

	if len(resolvedPattern) > 500 {
		return false, fmt.Errorf("format pattern too complex (max 500 chars)")
	}

	re, err := regexp.Compile(resolvedPattern)
	if err != nil {
		return false, fmt.Errorf("invalid format pattern: %v", err)
	}

	// Go doesn't have native regex timeout, but for simple BaaS this is usually fine.
	// For production we might want to use a third-party regex engine with timeouts if needed.
	if !re.MatchString(value) {
		hint := ""
		if preset, ok := FormatPresets[pattern]; ok {
			hint = fmt.Sprintf(" Expected format: %s", preset.Example)
		}
		return false, fmt.Errorf("value \"%s\" does not match the required format.%s", value, hint)
	}

	return true, nil
}

// --- DATABASE UTILS ---

// QuotePostgresLiteral escapes strings for PostgreSQL inline execution
func QuotePostgresLiteral(str string) string {
	if str == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(str, "'", "''") + "'"
}

// --- BYTE CONVERSION ---

// ParseBytes converts size strings (2MB, 1GB) to bytes
func ParseBytes(sizeStr string) int64 {
	if sizeStr == "" {
		return 2 * 1024 * 1024
	}

	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-zA-Z]+)?$`)
	match := re.FindStringSubmatch(sizeStr)
	if match == nil {
		val, _ := strconv.ParseInt(sizeStr, 10, 64)
		return val
	}

	num, _ := strconv.ParseFloat(match[1], 64)
	unit := strings.ToUpper(match[2])
	if unit == "" {
		unit = "B"
	}

	multipliers := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}

	return int64(math.Floor(num * float64(multipliers[unit])))
}

// FormatBytes converts bytes to human readable string
func FormatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 Bytes"
	}
	units := []string{"Bytes", "KB", "MB", "GB"}
	i := int(math.Floor(math.Log(float64(bytes)) / math.Log(1024)))
	if i >= len(units) {
		i = len(units) - 1
	}
	val := float64(bytes) / math.Pow(1024, float64(i))
	return fmt.Sprintf("%.2f %s", val, units[i])
}

// WalkAsync-like functionality in Go
type FileEntry struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at"`
	Path      string `json:"path"`
}

func WalkDir(dirPath, rootPath string) ([]FileEntry, error) {
	var results []FileEntry
	err := filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(rootPath, path)
		entryType := "file"
		if d.IsDir() {
			entryType = "folder"
		}

		results = append(results, FileEntry{
			Name:      d.Name(),
			Type:      entryType,
			Size:      info.Size(),
			UpdatedAt: info.ModTime().Format(time.RFC3339),
			Path:      filepath.ToSlash(rel),
		})

		return nil
	})
	return results, err
}

// ParseColumnFormat is now provided by sql.go in the same package

func BuildColumnComment(description, formatPattern string) string {
	if formatPattern == "" {
		return description
	}
	return fmt.Sprintf("%s||FORMAT:%s", description, formatPattern)
}
