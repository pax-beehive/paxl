package adaptor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type PiInstallation struct {
	SDKRoot    string
	Executable string
	Version    string
	Source     string
	Node       string
}

var errNotPiManaged = errors.New("not a Pi managed launcher")

var piManagedVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

// ResolvePiInstallation follows the chosen launcher only, including Pi's owned
// managed wrapper. It never searches another installation or executes the CLI.
func ResolvePiInstallation(selected string) (*PiInstallation, error) {
	launcher, err := exec.LookPath(selected)
	if err != nil {
		return nil, fmt.Errorf("pi is not installed; run paxl harness install pi: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		return nil, fmt.Errorf("resolve Pi executable: %w", err)
	}
	managed, err := resolveManagedPi(resolved)
	if !errors.Is(err, errNotPiManaged) {
		return managed, err
	}
	for dir := filepath.Dir(resolved); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		data, err := os.ReadFile(
			filepath.Join(dir, "package.json"),
		) // #nosec G304,G703 -- Metadata next to the selected launcher.
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Pi package metadata: %w", err)
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		if manifest.Name == "@earendil-works/pi-coding-agent" ||
			manifest.Name == "@mariozechner/pi-coding-agent" {
			return &PiInstallation{
				SDKRoot:    dir,
				Executable: resolved,
				Version:    manifest.Version,
				Source:     "npm",
				Node:       "node",
			}, nil
		}
	}
	return nil, fmt.Errorf(
		"selected Pi launcher has no supported SDK installation; use paxl harness install pi",
	)
}

func resolveManagedPi(launcher string) (*PiInstallation, error) {
	if filepath.Base(launcher) != "pi" || filepath.Base(filepath.Dir(launcher)) != "bin" {
		return nil, errNotPiManaged
	}
	agentDir := filepath.Dir(filepath.Dir(launcher))
	root := filepath.Join(agentDir, "install")
	data, err := os.ReadFile(
		filepath.Join(root, "managed-install.json"),
	) // #nosec G304,G703 -- Owner metadata next to selected Pi wrapper.
	if os.IsNotExist(err) {
		return nil, errNotPiManaged
	}
	if err != nil {
		return nil, fmt.Errorf("read Pi managed installation marker: %w", err)
	}
	var marker struct {
		Kind          string `json:"kind"`
		SchemaVersion int    `json:"schemaVersion"`
		Layout        string `json:"layout"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Kind != "pi-managed-install" ||
		marker.SchemaVersion != 1 ||
		marker.Layout != "releases-v1" {
		return nil, fmt.Errorf("invalid Pi managed installation marker")
	}
	data, err = os.ReadFile(
		filepath.Join(root, "current-version"),
	) // #nosec G304,G703 -- Owner metadata next to selected Pi wrapper.
	if err != nil {
		return nil, fmt.Errorf("read Pi managed current version: %w", err)
	}
	version := strings.TrimSpace(string(data))
	if !piManagedVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid Pi managed current version")
	}
	release := filepath.Join(root, "releases", version)
	executable, err := filepath.EvalSymlinks(filepath.Join(release, "node_modules", ".bin", "pi"))
	if err != nil {
		return nil, fmt.Errorf("resolve Pi managed release: %w", err)
	}
	sdkRoot := filepath.Join(release, "node_modules", "@earendil-works", "pi-coding-agent")
	data, err = os.ReadFile(
		filepath.Join(sdkRoot, "package.json"),
	) // #nosec G304,G703 -- Selected version validated before joining paths.
	if err != nil {
		return nil, fmt.Errorf("read Pi managed package: %w", err)
	}
	if err := validateManagedPiManifest(data, version); err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(sdkRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Pi managed package: %w", err)
	}
	relative, err := filepath.Rel(realRoot, executable)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("pi managed executable is outside selected SDK package")
	}
	node, err := managedPiNode()
	if err != nil {
		return nil, err
	}
	return &PiInstallation{
		SDKRoot:    realRoot,
		Executable: executable,
		Version:    version,
		Source:     "pi-managed",
		Node:       node,
	}, nil
}

func managedPiNode() (string, error) {
	node := "node"
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Pi runtime home: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	privateNode := filepath.Join(dataHome, "pi-node", "current", "bin", "node")
	if found, err := exec.LookPath(privateNode); err == nil {
		node = found
	}
	return node, nil
}

func validateManagedPiManifest(data []byte, version string) error {
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil ||
		manifest.Name != "@earendil-works/pi-coding-agent" ||
		manifest.Version != version {
		return fmt.Errorf("pi managed package does not match selected release")
	}
	return nil
}
