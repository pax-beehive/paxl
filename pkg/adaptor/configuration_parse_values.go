package adaptor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func configurationMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func configurationString(value any) string {
	result, _ := value.(string)
	return result
}

func configurationStrings(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case string:
		result = append(result, typed)
	case []string:
		result = append(result, typed...)
	case []any:
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func configurationBool(value any) *bool {
	if result, ok := value.(bool); ok {
		return &result
	}
	return nil
}

func configurationPointer(parts ...string) string {
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	if len(escaped) == 0 {
		return ""
	}
	return "/" + strings.Join(escaped, "/")
}

func configurationID(kind, name string, source *model.ConfigurationSource) string {
	identity := kind + "\x00" + name
	if source != nil {
		identity += "\x00" + source.Path + "\x00" + source.Pointer + "\x00" + string(source.Scope)
	}
	digest := sha256.Sum256([]byte(identity))
	return kind + ":" + hex.EncodeToString(digest[:16])
}
