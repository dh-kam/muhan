package legacycrypt

import (
	"crypto/subtle"
	"strings"
)

func Verify(password, stored string) bool {
	stored = strings.TrimRight(strings.TrimSpace(stored), "\x00")
	if stored == "" {
		return false
	}
	if IsBcryptHash(stored) {
		return VerifyBcrypt(password, stored)
	}
	hash, err := Hash(password)
	return err == nil && subtle.ConstantTimeCompare([]byte(hash), []byte(stored)) == 1
}
