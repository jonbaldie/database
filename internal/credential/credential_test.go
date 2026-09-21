package credential

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAccountName(t *testing.T) {
	for _, name := range []string{"a", "Z", "9", "admin", "Admin.ops_2-x", strings.Repeat("a", 32)} {
		if err := ValidateAccountName(name); err != nil {
			t.Errorf("ValidateAccountName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "_admin", ".admin", "-admin", "admin@localhost", "ad min", "admin'", strings.Repeat("a", 33), "aš", "éadmin"} {
		if err := ValidateAccountName(name); !errors.Is(err, ErrInvalidAccountName) {
			t.Errorf("ValidateAccountName(%q) = %v, want ErrInvalidAccountName", name, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for _, password := range []string{strings.Repeat("p", 12), strings.Repeat("p", 1024), "pässwörd-ünïcode"} {
		if err := ValidatePassword(password); err != nil {
			t.Errorf("ValidatePassword(%d bytes) = %v, want nil", len(password), err)
		}
	}
	for _, password := range []string{"", "secret", strings.Repeat("p", 11), strings.Repeat("p", 1025), "valid-prefix\xff"} {
		err := ValidatePassword(password)
		if !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("ValidatePassword(%d bytes) = %v, want ErrInvalidPassword", len(password), err)
		}
		if password != "" && err != nil && strings.Contains(err.Error(), password) {
			t.Errorf("ValidatePassword error reveals the password")
		}
	}
}

func TestPasswordHashIsLowercaseHexSHA256(t *testing.T) {
	const want = "ef92b778bafe771e89245b89ecbc08a44a4e166c06659911881f383d4473e94f"
	if got := PasswordHash("password123"); got != want {
		t.Fatalf("PasswordHash = %q, want %q", got, want)
	}
}

func TestPasswordMatches(t *testing.T) {
	hash := PasswordHash("contract-valid-password")
	if !PasswordMatches([]byte("contract-valid-password"), hash) {
		t.Fatal("PasswordMatches rejected the hashed password")
	}
	if !PasswordMatches([]byte("contract-valid-password"), strings.ToUpper(hash)) {
		t.Fatal("PasswordMatches rejected an uppercase stored hash")
	}
	for _, stored := range []string{"", "not-hex", hash[:62], PasswordHash("other-valid-password")} {
		if PasswordMatches([]byte("contract-valid-password"), stored) {
			t.Fatalf("PasswordMatches accepted stored hash %q", stored)
		}
	}
}
