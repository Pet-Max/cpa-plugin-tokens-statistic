package store

// DecryptFunc reveals one ciphertext as (plaintext, status); status carries a
// degradation reason when the plaintext cannot be restored.
type DecryptFunc func(ciphertext, fingerprint string, generation uint64) (string, string)

// Sensitive is implemented by response payloads that may carry encrypted API
// key material and know how to reveal it given a decrypt function.
type Sensitive interface {
	Redact()
	Reveal(decrypt DecryptFunc)
}
