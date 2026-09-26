package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func testKeyRaw() string { return "0123456789abcdef0123456789abcdef" } // 32 bytes

func TestNewCipherAcceptsKeyFormats(t *testing.T) {
	raw := testKeyRaw()
	cases := map[string]string{
		"raw":    raw,
		"hex":    hex.EncodeToString([]byte(raw)),
		"base64": base64.StdEncoding.EncodeToString([]byte(raw)),
	}
	for name, key := range cases {
		if _, err := NewCipher(key); err != nil {
			t.Errorf("%s key rejected: %v", name, err)
		}
	}
}

func TestNewCipherRejectsBadKey(t *testing.T) {
	for _, key := range []string{"", "short", "this-key-is-not-thirty-two-byte!!"} {
		if _, err := NewCipher(key); err == nil {
			t.Errorf("expected error for key %q", key)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := NewCipher(testKeyRaw())
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("super-secret-kubeconfig-token")

	ct, nonce, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := c.Decrypt(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip = %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c, _ := NewCipher(testKeyRaw())
	ct1, n1, _ := c.Encrypt([]byte("same"))
	ct2, n2, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonce reused across encryptions")
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("identical plaintext produced identical ciphertext")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	c, _ := NewCipher(testKeyRaw())
	ct, nonce, _ := c.Encrypt([]byte("secret"))
	ct[0] ^= 0xff
	if _, err := c.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected authentication failure on tampered ciphertext")
	}
}

func TestFingerprintStableAndOpaque(t *testing.T) {
	fp1 := Fingerprint([]byte("value"))
	fp2 := Fingerprint([]byte("value"))
	if fp1 != fp2 {
		t.Fatal("fingerprint not stable")
	}
	if fp1 == Fingerprint([]byte("other")) {
		t.Fatal("distinct values share a fingerprint")
	}
	if len(fp1) > 24 {
		t.Fatalf("fingerprint unexpectedly long: %q", fp1)
	}
}

func TestScopeAndKindValidation(t *testing.T) {
	if !ScopeProduction.Valid() || !ScopeRead.Valid() || !ScopeSandbox.Valid() {
		t.Fatal("known scopes should be valid")
	}
	if Scope("root").Valid() {
		t.Fatal("unknown scope should be invalid")
	}
	if !KindKubernetes.Valid() || Kind("s3").Valid() {
		t.Fatal("kind validation wrong")
	}
}
