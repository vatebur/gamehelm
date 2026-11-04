package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type jsonFieldSpan struct {
	keyStart   int
	keyEnd     int
	valueStart int
	valueEnd   int
}

// findField locates a direct object's field in comment-stripped JSON. Offsets
// also address the original JSONC because stripComments preserves byte lengths.
func findField(data []byte, name string) (jsonFieldSpan, error) {
	invalid := errors.New("无法定位配置中的认证字段")
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return jsonFieldSpan{}, invalid
	}
	for decoder.More() {
		beforeKey := int(decoder.InputOffset())
		key, err := decoder.Token()
		if err != nil {
			return jsonFieldSpan{}, invalid
		}
		keyEnd := int(decoder.InputOffset())
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return jsonFieldSpan{}, invalid
		}
		fieldName, ok := key.(string)
		if !ok || !strings.EqualFold(fieldName, name) {
			continue
		}
		keyOffset := bytes.IndexByte(data[beforeKey:keyEnd], '"')
		if keyOffset < 0 {
			return jsonFieldSpan{}, invalid
		}
		valueEnd := int(decoder.InputOffset())
		return jsonFieldSpan{
			keyStart:   beforeKey + keyOffset,
			keyEnd:     keyEnd,
			valueStart: valueEnd - len(value),
			valueEnd:   valueEnd,
		}, nil
	}
	return jsonFieldSpan{}, invalid
}

// rewritePassword edits only the active password key and string value. All
// comments, whitespace, field order and line endings outside those tokens stay
// byte-for-byte identical to the administrator's original configuration.
func rewritePassword(original []byte, hash string) ([]byte, error) {
	clean, err := stripComments(original)
	if err != nil {
		return nil, err
	}
	authField, err := findField(clean, "auth")
	if err != nil {
		return nil, err
	}
	password, err := findField(clean[authField.valueStart:authField.valueEnd], "password")
	if err != nil {
		return nil, err
	}
	encodedHash, err := json.Marshal(hash)
	if err != nil {
		return nil, errors.New("编码密码哈希失败")
	}
	base := authField.valueStart
	result := make([]byte, 0, len(original)+len(encodedHash))
	result = append(result, original[:base+password.keyStart]...)
	result = append(result, `"password_hash"`...)
	result = append(result, original[base+password.keyEnd:base+password.valueStart]...)
	result = append(result, encodedHash...)
	result = append(result, original[base+password.valueEnd:]...)
	if len(result) > maxConfigBytes {
		return nil, errors.New("密码回写后的配置文件超过 1 MiB")
	}
	return result, nil
}
