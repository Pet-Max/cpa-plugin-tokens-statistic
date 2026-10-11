// Package apikey implements the API key confidentiality engine: domain-split
// key derivation from the configured secret, AES-GCM sealing of API key
// ciphertexts bound to a generation and fingerprint, the HMAC fingerprints
// used as stable storage identifiers, and the g<generation>:<hash> reference
// format shared with the statistics store and management API.
//
// The package depends only on the standard library. Nothing here may import
// the host plugin SDK or the plugin's storage and HTTP layers; the dependency
// direction is strictly store/plugin -> apikey, never the reverse.
package apikey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	apiKeyCipherVersionV1 byte = 1
	apiKeyCipherVersionV2 byte = 2
	apiKeyCipherVersion        = apiKeyCipherVersionV1
	// DefaultSecret is the shipped default management secret. A context
	// derived from it stays functional but is flagged so the dashboard can
	// warn that anyone holding the database file can decrypt stored keys.
	DefaultSecret = "123456"
	// HashVersion names the fingerprint algorithm recorded with every stored
	// crypto generation.
	HashVersion = "hmac-sha256-128-v1"
)

// CryptoContext carries the key material derived from one configured secret.
type CryptoContext struct {
	EncKey            [32]byte
	IndexKey          [32]byte
	KeyID             string
	Enabled           bool
	UsesDefaultSecret bool
}

// APIKeyCryptoGeneration describes one key rotation generation persisted with
// the usage records it encrypted.
type APIKeyCryptoGeneration struct {
	ID              uint64    `json:"id"`
	KeyID           string    `json:"key_id,omitempty"`
	HashVersion     string    `json:"hash_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	IdentityMissing bool      `json:"identity_missing,omitempty"`
}

func deriveDomainKey(secret, domain string) [32]byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("tokens-statistic/" + domain + "/v1"))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func DeriveCryptoContext(secret string) (CryptoContext, error) {
	if secret == "" {
		return CryptoContext{}, nil
	}
	if secret != DefaultSecret && len([]byte(secret)) < 32 {
		return CryptoContext{}, errors.New("secret must be empty, 123456, or at least 32 bytes")
	}
	ctx := CryptoContext{
		EncKey:            deriveDomainKey(secret, "api-key-encryption"),
		IndexKey:          deriveDomainKey(secret, "api-key-index"),
		Enabled:           true,
		UsesDefaultSecret: secret == DefaultSecret,
	}
	idInput := append([]byte("tokens-statistic/key-id/v1:"), ctx.IndexKey[:]...)
	id := sha256.Sum256(idInput)
	ctx.KeyID = hex.EncodeToString(id[:16])
	return ctx, nil
}

func Fingerprint(apiKey string, indexKey [32]byte) string {
	if apiKey == "" {
		return ""
	}
	mac := hmac.New(sha256.New, indexKey[:])
	_, _ = mac.Write([]byte(apiKey))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func EncryptAPIKey(ctx CryptoContext, plaintext, fingerprint string) (string, error) {
	return encryptAPIKeyVersion(ctx, plaintext, fingerprint, 0, apiKeyCipherVersionV1)
}

func EncryptForGeneration(ctx CryptoContext, plaintext, fingerprint string, generation uint64) (string, error) {
	if generation == 0 {
		return "", errors.New("api key generation is required")
	}
	return encryptAPIKeyVersion(ctx, plaintext, fingerprint, generation, apiKeyCipherVersionV2)
}

func encryptAPIKeyVersion(ctx CryptoContext, plaintext, fingerprint string, generation uint64, version byte) (string, error) {
	if !ctx.Enabled || plaintext == "" || fingerprint == "" {
		return "", nil
	}
	block, err := aes.NewCipher(ctx.EncKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	aad, err := apiKeyAAD(version, generation, fingerprint)
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), aad)
	combined := make([]byte, 1, 1+len(nonce)+len(sealed))
	combined[0] = version
	combined = append(combined, nonce...)
	combined = append(combined, sealed...)
	return base64.RawURLEncoding.EncodeToString(combined), nil
}

func DecryptAPIKey(ctx CryptoContext, ciphertext, fingerprint string) (string, error) {
	return DecryptForGeneration(ctx, ciphertext, fingerprint, 0)
}

func DecryptForGeneration(ctx CryptoContext, ciphertext, fingerprint string, generation uint64) (string, error) {
	if !ctx.Enabled || ciphertext == "" || fingerprint == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(raw) < 1 {
		return "", errors.New("unsupported api key ciphertext version")
	}
	version := raw[0]
	raw = raw[1:]
	block, err := aes.NewCipher(ctx.EncKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize+gcm.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	aad, err := apiKeyAAD(version, generation, fingerprint)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], aad)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func apiKeyAAD(version byte, generation uint64, fingerprint string) ([]byte, error) {
	switch version {
	case apiKeyCipherVersionV1:
		return []byte("api-key/v1:" + fingerprint), nil
	case apiKeyCipherVersionV2:
		if generation == 0 {
			return nil, errors.New("api key generation is required for ciphertext v2")
		}
		return []byte(fmt.Sprintf("api-key/v2:%d:%s", generation, fingerprint)), nil
	default:
		return nil, errors.New("unsupported api key ciphertext version")
	}
}

func Ref(generation uint64, fingerprint string) string {
	if generation == 0 || !ValidHash(fingerprint) {
		return ""
	}
	return "g" + strconv.FormatUint(generation, 10) + ":" + fingerprint
}

func ParseRef(ref string) (uint64, string, bool) {
	prefix, hash, ok := strings.Cut(ref, ":")
	if !ok || len(prefix) < 2 || prefix[0] != 'g' || !ValidHash(hash) {
		return 0, "", false
	}
	generation, err := strconv.ParseUint(prefix[1:], 10, 64)
	if err != nil || generation == 0 || Ref(generation, hash) != ref {
		return 0, "", false
	}
	return generation, hash, true
}

func ValidHash(hash string) bool {
	if len(hash) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 16 && strings.ToLower(hash) == hash
}

// Status values reported by DecryptFunc when a ciphertext cannot be restored
// to plaintext; they double as the dashboard's API key display states.
const (
	StatusAvailable             = "available"
	StatusGenerationUnavailable = "generation_unavailable"
	StatusCiphertextMissing     = "ciphertext_missing"
	StatusCiphertextInvalid     = "ciphertext_invalid"
	StatusIdentityMissing       = "identity_missing"
	StatusSourceMissing         = "source_missing"
)
