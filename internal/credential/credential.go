// Package credential owns the database-account credential policy shared by
// operator initialization and the account-administration SQL contract.
package credential

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"unicode/utf8"
)

const (
	maxAccountNameBytes = 32
	minPasswordBytes    = 12
	maxPasswordBytes    = 1024
)

var (
	// ErrInvalidAccountName reports an account name outside the contract.
	ErrInvalidAccountName = errors.New("account name must be 1 through 32 bytes: an ASCII letter or digit, then ASCII letters, digits, '.', '_', or '-'")
	// ErrInvalidPassword reports a password outside the contract. It never
	// carries the password.
	ErrInvalidPassword = errors.New("password must be valid UTF-8 of 12 through 1024 bytes")
)

// ValidateAccountName applies the case-sensitive account-name rule.
func ValidateAccountName(name string) error {
	if len(name) == 0 || len(name) > maxAccountNameBytes || !asciiLetterOrDigit(name[0]) {
		return ErrInvalidAccountName
	}
	for _, character := range []byte(name[1:]) {
		if !asciiLetterOrDigit(character) && character != '.' && character != '_' && character != '-' {
			return ErrInvalidAccountName
		}
	}
	return nil
}

// ValidatePassword applies the length and encoding rule to a password.
func ValidatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) < minPasswordBytes || len(password) > maxPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

// PasswordHash returns the stored form of a password: lowercase hex SHA-256.
func PasswordHash(password string) string {
	digest := passwordDigest([]byte(password))
	return hex.EncodeToString(digest[:])
}

// PasswordMatches reports in constant time whether password has the stored
// hash encodedHash.
func PasswordMatches(password []byte, encodedHash string) bool {
	expected, err := hex.DecodeString(encodedHash)
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	actual := passwordDigest(password)
	return subtle.ConstantTimeCompare(actual[:], expected) == 1
}

func passwordDigest(password []byte) [sha256.Size]byte {
	return sha256.Sum256(password)
}

func asciiLetterOrDigit(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
}
