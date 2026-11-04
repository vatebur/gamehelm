package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vatebur/gamehelm/internal/auth"
)

func TestPreparePreservesCommentsAndFormatting(t *testing.T) {
	const password = "中文-secret-\"quoted\"-\\path-/*literal*/"
	encodedPassword, err := json.Marshal(password)
	if err != nil {
		t.Fatal(err)
	}
	const commented = `// 顶部说明，"auth": {"password": "comment-only"}
{
  "web": {
    "listen": "0.0.0.0:8231"
    // ,"public_origin": "https://services.example.com"
    // ,"trusted_proxies": ["127.0.0.1/32", "::1/128"]
  },
  "auth" /* 对象前的注释 */ : {
    // "password": "inactive-example"
    /* 更换口令 */ {{KEY}} /* 键后 */ : /* 值前 */ {{VALUE}} // 值后
  },
  "services": {
    "worker": {"display_name": "后台服务 // /*文字*/", "runtime": "systemd", "unit": "worker.service"}
  }
}
// 文件末尾说明
`
	for _, tc := range []struct {
		name     string
		template string
		key      string
	}{
		{"comments", commented, `"password"`},
		{"CRLF without final newline", strings.ReplaceAll(strings.TrimSuffix(commented, "\n"), "\n", "\r\n"), `"password"`},
		{"escaped keys", strings.Replace(commented, `  "auth" /*`, `  "\u0061uth" /*`, 1), `"\u0070assword"`},
		{"auth first and mixed case", `{"Auth":{/*inside*/{{KEY}}:{{VALUE}}},"services":{"worker":{"display_name":"worker","runtime":"systemd","unit":"worker.service"}}}`, `"PassWord"`},
		{"auth last", `{"services":{"worker":{"display_name":"worker","runtime":"systemd","unit":"worker.service"}},"auth":{ {{KEY}} : {{VALUE}} /* tail */ }}`, `"password"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := strings.NewReplacer("{{KEY}}", tc.key, "{{VALUE}}", string(encodedPassword)).Replace(tc.template)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
				t.Fatal(err)
			}
			cfg, release, err := Prepare(path)
			if err != nil {
				t.Fatal(err)
			}
			release()
			encodedHash, err := json.Marshal(cfg.Auth.PasswordHash)
			if err != nil {
				t.Fatal(err)
			}
			expected := strings.NewReplacer("{{KEY}}", `"password_hash"`, "{{VALUE}}", string(encodedHash)).Replace(tc.template)
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, []byte(expected)) {
				t.Fatal("rewrite changed bytes outside the active password key and value")
			}
			if bytes.Contains(actual, encodedPassword) {
				t.Fatal("active plaintext password remains in configuration")
			}
			loaded, err := Load(path)
			if err != nil || loaded.Auth.Password != "" || !auth.VerifyPassword(loaded.Auth.PasswordHash, password) {
				t.Fatal("rewritten credentials cannot authenticate the original password")
			}
			if loaded.Web.PublicOrigin != "" || len(loaded.Web.TrustedProxies) != 0 {
				t.Fatal("commented proxy settings became active")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("rewritten configuration permissions are not 0600")
			}
			_, againRelease, err := Prepare(path)
			if err != nil {
				t.Fatal(err)
			}
			againRelease()
			again, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, again) {
				t.Fatal("restart changed the hashed configuration")
			}
		})
	}
}

func TestPrepareRejectsFoldedDuplicateCredentials(t *testing.T) {
	original := []byte(`{"auth":{"password":"first-test-password","paſsword":"second-test-password"},"services":{"worker":{"display_name":"worker","runtime":"systemd","unit":"worker.service"}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, release, err := Prepare(path); err == nil {
		release()
		t.Fatal("accepted multiple credential keys with equivalent Unicode folding")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("invalid credential configuration was modified")
	}
}

func TestPrepareKeepsOriginalWhenHashExceedsConfigLimit(t *testing.T) {
	const config = `{"auth":{"password":"test-password"},"services":{"worker":{"display_name":"worker","runtime":"systemd","unit":"worker.service"}}}`
	original := []byte(config + strings.Repeat(" ", maxConfigBytes-len(config)))
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, release, err := Prepare(path); err == nil {
		release()
		t.Fatal("saved a configuration that exceeds the reader's size limit")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("failed password rewrite changed the original file")
	}
}
