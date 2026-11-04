package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTripAndBounds(t *testing.T) {
	hash, err := HashPassword("correct-horse-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHash(hash); err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-secret") || VerifyPassword(hash, "incorrect-horse-secret") {
		t.Fatal("password verification failed")
	}
	for _, invalid := range []string{
		strings.Replace(hash, "m=65536", "m=4294967295", 1),
		strings.Replace(hash, "t=3", "t=0", 1),
		strings.Replace(hash, "p=4", "p=256", 1),
		strings.Replace(hash, "v=19", "v=16", 1),
		strings.Replace(hash, "argon2id", "argon2i", 1),
		hash + "$extra",
	} {
		if ValidateHash(invalid) == nil || VerifyPassword(invalid, "correct-horse-secret") {
			t.Fatal("accepted invalid or excessive-cost password hash")
		}
	}
}

func TestPasswordInputLimits(t *testing.T) {
	for _, password := range []string{"", "changeme", strings.Repeat(" ", 12), strings.Repeat("a", MaxPasswordBytes+1)} {
		if ValidatePassword(password) == nil {
			t.Fatal("accepted invalid password")
		}
	}
	if err := ValidatePassword("这是一个满足长度要求的密码"); err != nil {
		t.Fatal(err)
	}
}
