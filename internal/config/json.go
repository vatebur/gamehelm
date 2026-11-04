package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// stripComments preserves byte offsets and newlines, including comments between
// tokens. Quoted URLs and escaped quotes remain untouched.
func stripComments(data []byte) ([]byte, error) {
	clean := bytes.Clone(data)
	inString := false
	for i := 0; i < len(clean); i++ {
		if inString {
			switch clean[i] {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		if clean[i] == '"' {
			inString = true
			continue
		}
		if clean[i] != '/' || i+1 >= len(clean) {
			continue
		}
		switch clean[i+1] {
		case '/':
			for i < len(clean) && clean[i] != '\n' {
				clean[i] = ' '
				i++
			}
		case '*':
			clean[i], clean[i+1] = ' ', ' '
			i += 2
			closed := false
			for i < len(clean) {
				if clean[i] == '*' && i+1 < len(clean) && clean[i+1] == '/' {
					clean[i], clean[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if clean[i] != '\n' && clean[i] != '\r' {
					clean[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, errors.New("配置文件的块注释未结束")
			}
		}
	}
	return clean, nil
}

func validateJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("配置必须是 JSON 对象")
	}
	if err := validateObject(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("配置文件包含多余内容")
	}
	return nil
}

func validateObject(decoder *json.Decoder, depth int) error {
	keys := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("配置 JSON 格式错误")
		}
		key, ok := token.(string)
		if !ok || keys[strings.ToLower(key)] {
			return errors.New("配置对象包含重复字段")
		}
		keys[strings.ToLower(key)] = true
		if err := validateValue(decoder, depth+1); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil || token != json.Delim('}') {
		return errors.New("配置 JSON 对象未结束")
	}
	return nil
}

func validateValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("配置 JSON 嵌套过深")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("配置 JSON 格式错误")
	}
	switch token {
	case json.Delim('{'):
		return validateObject(decoder, depth)
	case json.Delim('['):
		for decoder.More() {
			if err := validateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("配置 JSON 数组未结束")
		}
	}
	return nil
}
