package apikey

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCryptoContextAndAPIKeyCiphertext(t *testing.T) {
	secret := strings.Repeat("s", 32)
	ctx, err := DeriveCryptoContext(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !ctx.Enabled || ctx.UsesDefaultSecret || len(ctx.KeyID) != 32 {
		t.Fatalf("unexpected crypto context: %+v", ctx)
	}
	if ctx.EncKey == ctx.IndexKey {
		t.Fatal("domain-separated keys must differ")
	}

	key := "test-client-api-key"
	fingerprint := Fingerprint(key, ctx.IndexKey)
	if len(fingerprint) != 32 || fingerprint != strings.ToLower(fingerprint) || fingerprint != Fingerprint(key, ctx.IndexKey) {
		t.Fatalf("invalid deterministic fingerprint %q", fingerprint)
	}
	if fingerprint == Fingerprint(key+"-other", ctx.IndexKey) {
		t.Fatal("different API keys produced the same test fingerprint")
	}

	first, err := EncryptAPIKey(ctx, key, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptAPIKey(ctx, key, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("AES-GCM encryption reused a nonce")
	}
	plain, err := DecryptAPIKey(ctx, first, fingerprint)
	if err != nil || plain != key {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := DecryptAPIKey(ctx, first, Fingerprint("wrong", ctx.IndexKey)); err == nil {
		t.Fatal("ciphertext decrypted with the wrong fingerprint/AAD")
	}
	wrong, _ := DeriveCryptoContext(strings.Repeat("x", 32))
	if _, err := DecryptAPIKey(wrong, first, fingerprint); err == nil {
		t.Fatal("ciphertext decrypted with the wrong key")
	}

	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatal(err)
	}
	raw[0]++
	if _, err := DecryptAPIKey(ctx, base64.RawURLEncoding.EncodeToString(raw), fingerprint); err == nil {
		t.Fatal("unsupported ciphertext version was accepted")
	}
	if _, err := DecryptAPIKey(ctx, base64.RawURLEncoding.EncodeToString([]byte{apiKeyCipherVersion}), fingerprint); err == nil {
		t.Fatal("short ciphertext was accepted")
	}
}

func TestCryptoContextDefaultDisabledAndValidation(t *testing.T) {
	ctx, err := DeriveCryptoContext(DefaultSecret)
	if err != nil || !ctx.Enabled || !ctx.UsesDefaultSecret {
		t.Fatalf("default context = %+v, %v", ctx, err)
	}
	disabled, err := DeriveCryptoContext("")
	if err != nil || disabled.Enabled || disabled.KeyID != "" {
		t.Fatalf("disabled context = %+v, %v", disabled, err)
	}
	if _, err := DeriveCryptoContext("short-secret"); err == nil {
		t.Fatal("short custom secret was accepted")
	}
}
