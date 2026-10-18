package facade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Download completely before execution, so a failed transfer cannot execute a
// partial script. Installer prompts retain stdin rather than consuming a pipe.
func (f *HarnessInstallFacade) downloadOfficialInstaller(
	ctx context.Context,
	url string,
) (string, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("prepare official installer download: %w", err)
	}
	req.Header.Set("User-Agent", "paxl")
	client := f.client
	if client == nil {
		client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("official installer redirect must use HTTPS")
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many official installer redirects")
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("download official installer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download official installer: HTTP %d", resp.StatusCode)
	}
	const limit = 2 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", nil, fmt.Errorf("read official installer: %w", err)
	}
	if len(data) > limit || !strings.HasPrefix(string(data), "#!") {
		return "", nil, fmt.Errorf(
			"official installer response is oversized or is not a shell script",
		)
	}
	file, err := os.CreateTemp("", "paxl-official-install-*.sh")
	if err != nil {
		return "", nil, fmt.Errorf("create official installer temporary file: %w", err)
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write official installer: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close official installer: %w", err)
	}
	return file.Name(), cleanup, nil
}
