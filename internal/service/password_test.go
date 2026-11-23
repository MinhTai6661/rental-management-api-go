package service

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("not a PHC argon2id hash with the expected parameters: %s", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("the hash must not contain the password")
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("the right password must verify: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil || ok {
		t.Fatalf("a wrong password must not verify: ok=%v err=%v", ok, err)
	}
}

func TestHashPasswordUsesAFreshSalt(t *testing.T) {
	a, _ := HashPassword("same password")
	b, _ := HashPassword("same password")
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

// A hash written with other parameters (an older release, a future one) still verifies: the parameters come from
// the stored string, not from the constants.
func TestVerifyPasswordReadsTheParametersFromTheHash(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("secret-pw"), salt, 1, 8*1024, 2, 32)
	b64 := base64.RawStdEncoding
	encoded := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=2$%s$%s", b64.EncodeToString(salt), b64.EncodeToString(key))

	if ok, err := VerifyPassword("secret-pw", encoded); err != nil || !ok {
		t.Fatalf("hash with other parameters: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("other", encoded); ok {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyPasswordRefusesUnusableHashes(t *testing.T) {
	good, _ := HashPassword("pw-12345678")
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":             "",
		"not phc":           "plaintext",
		"bcrypt":            "$2y$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234",
		"argon2i":           strings.Replace(good, "argon2id", "argon2i", 1),
		"old version":       strings.Replace(good, "v=19", "v=16", 1),
		"missing part":      "$argon2id$v=19$m=19456,t=2,p=1$" + parts[4],
		"unknown parameter": strings.Replace(good, "p=1", "x=1", 1),
		"non numeric":       strings.Replace(good, "m=19456", "m=lots", 1),
		"zero time":         strings.Replace(good, "t=2", "t=0", 1),
		"memory too big":    strings.Replace(good, "m=19456", "m=4194304", 1),
		"too many passes":   strings.Replace(good, "t=2", "t=99", 1),
		"too many lanes":    strings.Replace(good, "p=1", "p=200", 1),
		"salt not base64":   "$argon2id$v=19$m=19456,t=2,p=1$!!!$" + parts[5],
		"hash not base64":   "$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$!!!",
		"empty salt":        "$argon2id$v=19$m=19456,t=2,p=1$$" + parts[5],
	}
	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifyPassword("pw-12345678", hash)
			if ok || err == nil {
				t.Errorf("an unusable hash must give an error and never a match: ok=%v err=%v", ok, err)
			}
		})
	}
}
