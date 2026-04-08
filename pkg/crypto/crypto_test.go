package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	// Generate a valid 32-byte AES-256 key in hex.
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	plaintext := "access-sandbox-abc123"
	encrypted, err := EncryptToken(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == plaintext {
		t.Error("encrypted should differ from plaintext")
	}

	decrypted, err := DecryptToken(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptDecrypt_EmptyKeyPassthrough(t *testing.T) {
	plaintext := "access-sandbox-abc123"

	encrypted, err := EncryptToken(plaintext, "")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted != plaintext {
		t.Error("with empty key, encrypted should equal plaintext")
	}

	decrypted, err := DecryptToken(plaintext, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Error("with empty key, decrypted should equal plaintext")
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	key1 := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	key2 := hex.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))

	encrypted, err := EncryptToken("secret", key1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = DecryptToken(encrypted, key2)
	if err == nil {
		t.Error("expected error decrypting with wrong key")
	}
}

func TestEncrypt_InvalidHexKey(t *testing.T) {
	_, err := EncryptToken("secret", "not-valid-hex")
	if err == nil {
		t.Error("expected error for invalid hex key")
	}
}

func TestDecrypt_InvalidHexKey(t *testing.T) {
	_, err := DecryptToken("somedata", "not-valid-hex")
	if err == nil {
		t.Error("expected error for invalid hex key")
	}
}

func TestEncrypt_ShortKey(t *testing.T) {
	// A key that is valid hex but not a valid AES key size (not 16/24/32 bytes).
	shortKey := hex.EncodeToString([]byte("short"))

	_, err := EncryptToken("secret", shortKey)
	if err == nil {
		t.Error("expected error for short AES key")
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	_, err := DecryptToken("not!valid!base64===", key)
	if err == nil {
		t.Error("expected error for invalid base64 ciphertext")
	}
}

func TestDecrypt_TruncatedCiphertext(t *testing.T) {
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	encrypted, err := EncryptToken("secret", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Truncate the ciphertext to be shorter than nonce size.
	truncated := encrypted[:4]
	_, err = DecryptToken(truncated, key)
	if err == nil {
		t.Error("expected error for truncated ciphertext")
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	encrypted, err := EncryptToken("", key)
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}

	decrypted, err := DecryptToken(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt empty: %v", err)
	}
	if decrypted != "" {
		t.Errorf("expected empty string, got %q", decrypted)
	}
}

func TestEncryptDecrypt_LongPlaintext(t *testing.T) {
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	plaintext := strings.Repeat("access-token-", 100)

	encrypted, err := EncryptToken(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := DecryptToken(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Error("round-trip failed for long plaintext")
	}
}
