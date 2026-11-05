package adaptor

import (
	"net/url"
	"strings"
)

const configurationRedacted = "[REDACTED]"

func (r *configurationReader) redactConfiguration() {
	for index := range r.result.MCPServers {
		server := &r.result.MCPServers[index]
		server.Command = configurationRedactCommand(server.Command)
		server.URL = configurationRedactURL(server.URL)
		for index := range server.Args {
			server.Args[index] = configurationRedactValue(server.Args[index])
		}
		for key, value := range server.Environment {
			server.Environment[key] = configurationRedactValue(value)
		}
		for key, value := range server.Headers {
			server.Headers[key] = configurationRedactValue(value)
		}
		server.Native = configurationRedactMap(server.Native, 0)
		server.Origin.URL = configurationRedactURL(server.Origin.URL)
		server.Redacted = true
	}
	for index := range r.result.Skills {
		skill := &r.result.Skills[index]
		skill.Frontmatter = configurationRedactSkillMetadata(skill.Frontmatter)
		skill.Origin.URL = configurationRedactURL(skill.Origin.URL)
	}
}

// Only references are safe to preserve in fields that can carry arbitrary
// credentials. They are never expanded against the process environment.
func configurationRedactValue(value string) string {
	if value == "" || configurationIsReference(value) {
		return value
	}
	return configurationRedacted
}

func configurationIsReference(value string) bool {
	for _, prefix := range []string{"${", "{env:", "{file:"} {
		if strings.HasPrefix(value, prefix) && strings.HasSuffix(value, "}") {
			inner := value[len(prefix) : len(value)-1]
			if inner != "" && !strings.ContainsAny(inner, "{}\r\n") &&
				!strings.Contains(inner, ":-") {
				return true
			}
		}
	}
	return false
}

func configurationRedactURL(value string) string {
	if value == "" || configurationIsReference(value) {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return configurationRedacted
	}
	parsed.User, parsed.Fragment, parsed.RawFragment = nil, "", ""
	if parsed.RawQuery != "" {
		query := parsed.Query()
		for key := range query {
			query[key] = []string{configurationRedacted}
		}
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func configurationRedactCommand(value string) string {
	// Commands are executable paths, not shell programs. Preserve paths with
	// spaces, but withhold inline shell fragments and embedded assignments.
	if strings.ContainsAny(value, "\r\n;|`") || strings.Contains(value, " -c ") ||
		strings.Contains(value, " -lc ") ||
		strings.Contains(value, "=") {
		return configurationRedacted
	}
	return value
}

func configurationRedactMap(values map[string]any, depth int) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = configurationRedactNative(key, value, depth+1)
	}
	return result
}

func configurationRedactNative(key string, value any, depth int) any {
	if depth > 64 {
		return configurationRedacted
	}
	if configurationSecretKey(key) {
		if text, ok := value.(string); ok {
			return configurationRedactValue(text)
		}
		return configurationRedacted
	}
	switch typed := value.(type) {
	case map[string]any:
		if key == "env" || key == "environment" || key == "headers" || key == "http_headers" {
			result := make(map[string]any, len(typed))
			for name, entry := range typed {
				text, ok := entry.(string)
				if ok {
					result[name] = configurationRedactValue(text)
				} else {
					result[name] = configurationRedacted
				}
			}
			return result
		}
		return configurationRedactMap(typed, depth)
	case []any:
		result := make([]any, len(typed))
		for index, entry := range typed {
			if key == "command" && index > 0 {
				if text, ok := entry.(string); ok {
					result[index] = configurationRedactValue(text)
				} else {
					result[index] = configurationRedacted
				}
			} else {
				result[index] = configurationRedactNative(key, entry, depth+1)
			}
		}
		return result
	case []string:
		result := make([]string, len(typed))
		for index, entry := range typed {
			result[index] = configurationRedactValue(entry)
		}
		return result
	case string:
		return configurationRedactNativeString(key, typed)
	default:
		return value
	}
}

func configurationRedactNativeString(key, value string) string {
	switch strings.ToLower(key) {
	case "command":
		return configurationRedactCommand(value)
	case "url", "serverurl", "source_url", "registry":
		return configurationRedactURL(value)
	case "name", "servername", "type", "transport", "cwd", "unresolved":
		return value
	default:
		return configurationRedactValue(value)
	}
}

func configurationSecretKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{"token", "secret", "password", "credential", "authorization", "api_key", "apikey", "bearer"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func configurationRedactSkillMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if configurationSecretKey(key) || key == "env" || key == "environment" || key == "headers" {
			result[key] = configurationRedactNative(key, value, 0)
		} else if nested, ok := value.(map[string]any); ok {
			result[key] = configurationRedactSkillMetadata(nested)
		} else if entries, ok := value.([]any); ok {
			result[key] = configurationRedactSkillEntries(entries)
		} else {
			result[key] = value
		}
	}
	return result
}

func configurationRedactSkillEntries(entries []any) []any {
	result := make([]any, len(entries))
	for index, entry := range entries {
		if nested, ok := entry.(map[string]any); ok {
			result[index] = configurationRedactSkillMetadata(nested)
		} else if nested, ok := entry.([]any); ok {
			result[index] = configurationRedactSkillEntries(nested)
		} else {
			result[index] = entry
		}
	}
	return result
}
