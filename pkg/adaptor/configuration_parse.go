package adaptor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"
)

var errUnsupportedConfigurationFormat = errors.New("unsupported configuration format")

func parseConfigurationDocument(data []byte, format string) (map[string]any, error) {
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if format == "json5" {
		return parseConfigurationJSON5(data)
	}
	if format == "yaml" || format == "yml" || format == "frontmatter" {
		return parseConfigurationYAML(data)
	}
	if format == "toml" {
		var document map[string]any
		if err := toml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("invalid TOML configuration")
		}
		if document == nil {
			document = map[string]any{}
		}
		return document, nil
	}
	if format == "jsonc" {
		var err error
		data, err = configurationJSONC(data)
		if err != nil {
			return nil, err
		}
	} else if format != "json" {
		return nil, fmt.Errorf("%w %q", errUnsupportedConfigurationFormat, format)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("invalid %s configuration", format)
	}
	if document == nil {
		return nil, fmt.Errorf("configuration must be an object")
	}
	return document, nil
}

func parseConfigurationJSON5(data []byte) (map[string]any, error) {
	var document map[string]any
	if err := json5.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("invalid JSON5 configuration")
	}
	if document == nil {
		return nil, fmt.Errorf("configuration must be an object")
	}
	if _, err := json.Marshal(document); err != nil {
		return nil, fmt.Errorf(
			"JSON5 configuration contains values that cannot be represented in JSON",
		)
	}
	return document, nil
}

func parseConfigurationYAML(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var document map[string]any
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("invalid YAML configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("configuration must contain one YAML document")
	}
	if document == nil {
		document = map[string]any{}
	}
	if _, err := json.Marshal(document); err != nil {
		return nil, fmt.Errorf(
			"YAML configuration contains values that cannot be represented in JSON",
		)
	}
	return document, nil
}

// Replace comments with whitespace so JSON offsets and line numbers remain useful.
func configurationJSONC(data []byte) ([]byte, error) {
	result := append([]byte(nil), data...)
	for index := 0; index < len(result); index++ {
		if result[index] == '"' {
			end, err := configurationJSONStringEnd(result, index)
			if err != nil {
				return nil, err
			}
			index = end
			continue
		}
		if result[index] != '/' || index+1 >= len(result) {
			continue
		}
		switch result[index+1] {
		case '/':
			for index < len(result) && result[index] != '\n' {
				result[index] = ' '
				index++
			}
		case '*':
			end := strings.Index(string(result[index+2:]), "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated JSON comment")
			}
			end += index + 3
			for index <= end {
				if result[index] != '\n' && result[index] != '\r' {
					result[index] = ' '
				}
				index++
			}
			index--
		}
	}
	return configurationJSONTrailingCommas(result)
}

func configurationJSONStringEnd(data []byte, start int) (int, error) {
	for index := start + 1; index < len(data); index++ {
		switch data[index] {
		case '\\':
			index++
		case '"':
			return index, nil
		}
	}
	return 0, fmt.Errorf("unterminated JSON string")
}

func configurationJSONTrailingCommas(data []byte) ([]byte, error) {
	for index := 0; index < len(data); index++ {
		switch data[index] {
		case '"':
			end, err := configurationJSONStringEnd(data, index)
			if err != nil {
				return nil, err
			}
			index = end
		case ',':
			next := index + 1
			for next < len(data) && strings.ContainsRune(" \t\r\n", rune(data[next])) {
				next++
			}
			if next < len(data) && (data[next] == '}' || data[next] == ']') {
				data[index] = ' '
			}
		}
	}
	return data, nil
}
