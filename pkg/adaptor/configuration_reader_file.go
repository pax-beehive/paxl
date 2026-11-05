package adaptor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

const configurationFileLimit = 4 << 20

func (r *configurationReader) readFile(
	path string,
	scope model.ConfigurationScope,
	format string,
) ([]byte, model.ConfigurationSource, bool) {
	source := model.ConfigurationSource{
		Path:   r.expandPath(path, r.cwd),
		Scope:  scope,
		Format: format,
		Owner:  string(r.req.Agent),
		Status: model.ConfigurationSourceStatusUnknown,
	}
	data, err := r.readConfigurationFile(&source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			source.Status = model.ConfigurationSourceStatusMissing
		} else {
			source.Status = model.ConfigurationSourceStatusError
			r.addDiagnostic("configuration_read_error", err.Error(), &source)
		}
		r.result.Sources = append(r.result.Sources, source)
		return nil, source, false
	}
	source.Status = model.ConfigurationSourceStatusRead
	digest := sha256.Sum256(data)
	source.SHA256 = hex.EncodeToString(digest[:])
	r.result.Sources = append(r.result.Sources, source)
	return data, source, true
}

func (r *configurationReader) readConfigurationFile(
	source *model.ConfigurationSource,
) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, fmt.Errorf("configuration read cancelled: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(source.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve configuration file: %w", err)
	}
	source.ResolvedPath = resolved
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect configuration file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration path is not a regular file")
	}
	if info.Size() > configurationFileLimit {
		return nil, fmt.Errorf(
			"configuration file exceeds the %d-byte limit",
			configurationFileLimit,
		)
	}
	file, err := openConfigurationFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("open configuration file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("opened configuration path is not a regular file")
	}
	return r.readConfigurationBytes(file)
}

func (r *configurationReader) readConfigurationBytes(file *os.File) ([]byte, error) {
	reader := io.LimitReader(file, configurationFileLimit+1)
	data := make([]byte, 0, 4096)
	buffer := make([]byte, 32*1024)
	for {
		if err := r.ctx.Err(); err != nil {
			return nil, fmt.Errorf("configuration read cancelled: %w", err)
		}
		count, err := reader.Read(buffer)
		data = append(data, buffer[:count]...)
		if len(data) > configurationFileLimit {
			return nil, fmt.Errorf(
				"configuration file exceeds the %d-byte limit",
				configurationFileLimit,
			)
		}
		if errors.Is(err, io.EOF) {
			return data, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read configuration file: %w", err)
		}
	}
}

func (r *configurationReader) readDocument(
	path string,
	scope model.ConfigurationScope,
) (map[string]any, model.ConfigurationSource, bool) {
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if r.req.Agent == model.AgentNameOpenClaw && (format == "json" || format == "jsonc") {
		format = "json5"
	}
	data, source, ok := r.readFile(path, scope, format)
	if !ok {
		return nil, source, false
	}
	document, err := parseConfigurationDocument(data, format)
	if err != nil {
		source.Status = model.ConfigurationSourceStatusError
		code := "configuration_parse_error"
		if errors.Is(err, errUnsupportedConfigurationFormat) {
			source.Status = model.ConfigurationSourceStatusUnsupported
			code = "configuration_format_unsupported"
		}
		r.result.Sources[len(r.result.Sources)-1] = source
		r.addDiagnostic(code, err.Error(), &source)
		return nil, source, false
	}
	return document, source, true
}

func (r *configurationReader) addDiagnostic(
	code, message string,
	source *model.ConfigurationSource,
) {
	var normalized *model.ConfigurationSource
	if source != nil {
		copySource := *source
		normalizeConfigurationSource(&copySource)
		normalized = &copySource
	}
	r.result.Diagnostics = append(
		r.result.Diagnostics,
		model.ConfigurationDiagnostic{
			Severity: "warning",
			Code:     code,
			Message:  message,
			Source:   normalized,
		},
	)
}

func normalizeConfigurationSource(source *model.ConfigurationSource) {
	if source.Scope == "" {
		source.Scope = model.ConfigurationScopeUnknown
	}
	if source.Format == "" {
		source.Format = "unknown"
	}
	if source.Status == "" {
		source.Status = model.ConfigurationSourceStatusUnknown
	}
}
