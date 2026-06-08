package keystore

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/hub-unibloom/cascata/crypto-engine/internal/crypto"
	"github.com/hub-unibloom/cascata/crypto-engine/internal/kek"
)

type KeyEntry struct {
	Version int    `json:"version"`
	Key     []byte `json:"key"` // 32 bytes (AES-256)
}

type Store struct {
	Keys map[string][]KeyEntry `json:"keys"`
}

type Manager struct {
	storePath string
	kek       []byte
	store     *Store
	mu        sync.RWMutex
	Sealed    bool
}

func (m *Manager) IsSealed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Sealed
}

func NewManager(path string, kek []byte) (*Manager, error) {
	m := &Manager{
		storePath: path,
		kek:       kek,
		store:     &Store{Keys: make(map[string][]KeyEntry)},
		Sealed:    len(kek) == 0,
	}

	// Se estiver selado (sem KEK no boot), paramos aqui.
	if m.Sealed {
		return m, nil
	}

	err := m.load()
	if err != nil {
		if os.IsNotExist(err) {
			// Se não existir, inicializamos chaves padrão
			fmt.Println("[KeyStore] Database not found. Creating fresh keys...")
			return m, m.initDefaults()
		}
		
		// ERRO DE DECRYPT: Entrar em modo SEALED em vez de falhar
		// Isso permite que o container continue saudável enquanto o admin resolve a chave
		if strings.Contains(err.Error(), "decryption failed") {
			fmt.Println("[KeyStore] WARNING: Failed to decrypt keystore with provided key.")
			fmt.Println("[KeyStore] Entering SEALED mode. Use /v1/unseal API to provide correct key.")
			m.Sealed = true
			m.kek = nil
			return m, nil
		}
		
		return nil, err
	}

	return m, nil
}

// Unlock abre o cofre fornecendo a Master Secret
func (m *Manager) Unlock(masterSecret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.Sealed {
		return fmt.Errorf("keyStore is already unsealed")
	}

	kekBytes, err := kek.DeriveKEK(masterSecret)
	if err != nil {
		return fmt.Errorf("failed to derive KEK from provided secret: %w", err)
	}

	// Tentativa de carregar a store com a nova chave
	oldKek := m.kek
	m.kek = kekBytes
	
	err = m.load()
	if err != nil {
		// Se o arquivo não existir, inicializamos com a nova KEK
		if os.IsNotExist(err) {
			err = m.initDefaults()
			if err == nil {
				m.Sealed = false
			}
			return err
		}
		
		// Se for erro de decryption, a chave está errada - reverter KEK
		if strings.Contains(err.Error(), "decryption failed") {
			m.kek = oldKek
			return fmt.Errorf("falha ao abrir KeyStore (Chave Mestra incorreta?): %w", err)
		}
		
		// Outros erros: reverter e retornar
		m.kek = oldKek
		return fmt.Errorf("falha ao abrir KeyStore: %w", err)
	}

	m.Sealed = false
	return nil
}

// Rekey troca a Master Secret decifrando os dados com a antiga e cifrando com a nova.
func (m *Manager) Rekey(oldSecret, newSecret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Sealed {
		return fmt.Errorf("keyStore is sealed. Unseal first to rekey")
	}

	// 1. Validar a segredo antigo (opcional se já unsealed, mas bom para dupla checagem)
	oldKek, err := kek.DeriveKEK(oldSecret)
	if err != nil {
		return fmt.Errorf("failed to derive old KEK: %w", err)
	}

	// Comparar com a KEK atual em memória
	for i := range oldKek {
		if oldKek[i] != m.kek[i] {
			return fmt.Errorf("old master secret is incorrect")
		}
	}

	// 2. Derivar a nova KEK
	newKek, err := kek.DeriveKEK(newSecret)
	if err != nil {
		return fmt.Errorf("failed to derive new KEK: %w", err)
	}

	// 3. Trocar e salvar
	originalKek := m.kek
	m.kek = newKek

	err = m.save()
	if err != nil {
		m.kek = originalKek // Reverte em caso de erro de escrita
		return fmt.Errorf("failed to save keystore with new key: %w", err)
	}

	return nil
}

func (m *Manager) initDefaults() error {
	_, err := m.generateKeyNoLock("system")
	if err != nil {
		return err
	}
	_, err = m.generateKeyNoLock("backup")
	return err
}

func (m *Manager) GetKey(name string, version int) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.Sealed {
		return nil, fmt.Errorf("operation forbidden: KeyStore is currently SEALED")
	}

	entries, ok := m.store.Keys[name]
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("key '%s' not found", name)
	}

	// Se version for 0, retorna a mais recente
	if version <= 0 {
		return entries[len(entries)-1].Key, nil
	}

	for _, e := range entries {
		if e.Version == version {
			return e.Key, nil
		}
	}

	return nil, fmt.Errorf("version %d for key '%s' not found", version, name)
}

func (m *Manager) GetLatestVersion(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.Sealed {
		return 0
	}

	entries, ok := m.store.Keys[name]
	if !ok || len(entries) == 0 {
		return 0
	}
	return entries[len(entries)-1].Version
}

func (m *Manager) GenerateKey(name string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Sealed {
		return 0, fmt.Errorf("operation forbidden: KeyStore is currently SEALED")
	}

	return m.generateKeyNoLock(name)
}

func (m *Manager) generateKeyNoLock(name string) (int, error) {
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return 0, err
	}

	entries := m.store.Keys[name]
	newVersion := 1
	if len(entries) > 0 {
		newVersion = entries[len(entries)-1].Version + 1
	}

	m.store.Keys[name] = append(entries, KeyEntry{
		Version: newVersion,
		Key:     newKey,
	})

	err := m.save()
	if err != nil {
		return 0, err
	}

	return newVersion, nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.storePath)
	if err != nil {
		return err
	}

	// Decifra a store com a KEK
	plaintext, err := crypto.DecryptAESGCM(data, m.kek)
	if err != nil {
		return fmt.Errorf("failed to decrypt keystore (WRONG MASTER SECRET?): %w", err)
	}

	return json.Unmarshal(plaintext, &m.store)
}

func (m *Manager) save() error {
	plaintext, err := json.Marshal(m.store)
	if err != nil {
		return err
	}

	// Cifra a store com a KEK antes de salvar
	ciphertext, err := crypto.EncryptAESGCM(plaintext, m.kek)
	if err != nil {
		return err
	}

	// Escrita atômica (salva em .tmp e renomeia) para evitar corrupção em caso de queda de energia
	tmpPath := m.storePath + ".tmp"
	err = os.WriteFile(tmpPath, ciphertext, 0600)
	if err != nil {
		return err
	}

	return os.Rename(tmpPath, m.storePath)
}

func (m *Manager) GetChecksum() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.Sealed || len(m.kek) == 0 {
		return "sealed"
	}

	// Retorna um hash curto da KEK (estável por Master Secret)
	hash := crypto.HashSHA256(m.kek)
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

// SecretEntry representa um segredo armazenado no KeyStore
type SecretEntry struct {
	Name      string `json:"name"`
	Encrypted string `json:"encrypted"` // Valor criptografado no formato cse:v1:keyName:version:base64
}

// SetSecret armazena um segredo criptografado no KeyStore
// O valor já deve estar no formato cse:v1:keyName:version:base64_ciphertext
func (m *Manager) SetSecret(name string, encryptedValue string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Sealed {
		return fmt.Errorf("operation forbidden: KeyStore is currently SEALED")
	}

	// Sanitização do nome
	if strings.ContainsAny(name, "/\\..$#@!%*") || name == "" {
		return fmt.Errorf("invalid secret name")
	}

	// Chave específica para segredos: "secret_<name>"
	secretKeyName := fmt.Sprintf("secret_%s", name)

	// Gera nova versão ou pega a existente
	entries := m.store.Keys[secretKeyName]
	newVersion := 1
	if len(entries) > 0 {
		newVersion = entries[len(entries)-1].Version + 1
	}

	// O valor criptografado é armazenado como se fosse uma chave
	// Usamos a própria estrutura KeyEntry para armazenar segredos
	// O campo Key contém o valor criptografado (em bytes)
	encryptedBytes := []byte(encryptedValue)

	m.store.Keys[secretKeyName] = append(entries, KeyEntry{
		Version: newVersion,
		Key:     encryptedBytes,
	})

	return m.save()
}

// GetSecret recupera um segredo do KeyStore
// Retorna o valor criptografado no formato cse:v1:keyName:version:base64
func (m *Manager) GetSecret(name string) (string, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.Sealed {
		return "", 0, fmt.Errorf("operation forbidden: KeyStore is currently SEALED")
	}

	// Sanitização do nome
	if strings.ContainsAny(name, "/\\..$#@!%*") || name == "" {
		return "", 0, fmt.Errorf("invalid secret name")
	}

	// Chave específica para segredos
	secretKeyName := fmt.Sprintf("secret_%s", name)

	entries, ok := m.store.Keys[secretKeyName]
	if !ok || len(entries) == 0 {
		return "", 0, fmt.Errorf("secret '%s' not found", name)
	}

	// Retorna a versão mais recente
	latestEntry := entries[len(entries)-1]
	return string(latestEntry.Key), latestEntry.Version, nil
}

// ListSecrets retorna a lista de nomes de segredos armazenados
func (m *Manager) ListSecrets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.Sealed {
		return []string{}
	}

	secrets := []string{}
	for keyName := range m.store.Keys {
		if strings.HasPrefix(keyName, "secret_") {
			secretName := strings.TrimPrefix(keyName, "secret_")
			secrets = append(secrets, secretName)
		}
	}
	return secrets
}

// DeleteSecret remove um segredo do KeyStore
func (m *Manager) DeleteSecret(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Sealed {
		return fmt.Errorf("operation forbidden: KeyStore is currently SEALED")
	}

	// Sanitização do nome
	if strings.ContainsAny(name, "/\\..$#@!%*") || name == "" {
		return fmt.Errorf("invalid secret name")
	}

	secretKeyName := fmt.Sprintf("secret_%s", name)

	if _, ok := m.store.Keys[secretKeyName]; !ok {
		return fmt.Errorf("secret '%s' not found", name)
	}

	delete(m.store.Keys, secretKeyName)
	return m.save()
}
