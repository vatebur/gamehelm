package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const MaxPasswordBytes = 1024

type passwordHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	digest      []byte
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > MaxPasswordBytes {
		return errors.New("密码至少需要 12 个字符，且不能超过 1024 字节")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("密码不能仅包含空白字符")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("生成密码盐失败")
	}
	digest := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func ValidateHash(encoded string) error {
	_, err := parseHash(encoded)
	return err
}

func VerifyPassword(encoded, password string) bool {
	if len(password) > MaxPasswordBytes {
		return false
	}
	hash, err := parseHash(encoded)
	if err != nil {
		return false
	}
	digest := argon2.IDKey([]byte(password), hash.salt, hash.iterations, hash.memory, hash.parallelism, uint32(len(hash.digest)))
	return subtle.ConstantTimeCompare(digest, hash.digest) == 1
}

func parseHash(encoded string) (passwordHash, error) {
	invalid := errors.New("password_hash 不是支持的 Argon2id 哈希或成本参数超出范围")
	if len(encoded) > 512 {
		return passwordHash{}, invalid
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return passwordHash{}, invalid
	}
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return passwordHash{}, invalid
	}
	values := make([]uint64, 3)
	for i, prefix := range []string{"m=", "t=", "p="} {
		value, ok := strings.CutPrefix(parameters[i], prefix)
		if !ok || value == "" {
			return passwordHash{}, invalid
		}
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return passwordHash{}, invalid
		}
		values[i] = number
	}
	if values[0] < 19*1024 || values[0] > 256*1024 || values[1] < 2 || values[1] > 10 || values[2] < 1 || values[2] > 16 {
		return passwordHash{}, invalid
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return passwordHash{}, invalid
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(digest) < 32 || len(digest) > 64 {
		return passwordHash{}, invalid
	}
	return passwordHash{memory: uint32(values[0]), iterations: uint32(values[1]), parallelism: uint8(values[2]), salt: salt, digest: digest}, nil
}
