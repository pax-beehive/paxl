package facade

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

type shellConfigFingerprint struct {
	digest [sha256.Size]byte
	exists bool
	known  bool
}

// Observe conventional user startup files only. Never execute their contents or
// retain them in results; unreadable or oversized files are not comparable.
func snapshotShellConfigs() map[string]shellConfigFingerprint {
	paths := make(map[string]shellConfigFingerprint)
	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	for _, name := range []string{".profile", ".bashrc", ".bash_profile", ".bash_login", ".ashrc", ".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		paths[filepath.Join(home, name)] = shellConfigFingerprint{}
	}
	if dir := os.Getenv("ZDOTDIR"); filepath.IsAbs(dir) {
		for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
			paths[filepath.Join(dir, name)] = shellConfigFingerprint{}
		}
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	// Several publishers hardcode ~/.config even when XDG_CONFIG_HOME is set.
	for _, dir := range []string{filepath.Join(home, ".config"), config} {
		for _, name := range []string{
			"fish/config.fish", "fish/conf.d/openclaw.fish",
			"bash/.bashrc", "bash/.bash_profile", "zsh/.zshrc", "zsh/.zshenv",
		} {
			paths[filepath.Join(dir, filepath.FromSlash(name))] = shellConfigFingerprint{}
		}
	}
	// OpenCode's sh/ash branch can select /etc/profile as a final fallback.
	if runtime.GOOS != "windows" {
		paths["/etc/profile"] = shellConfigFingerprint{}
	}
	for path := range paths {
		paths[path] = fingerprintShellConfig(path)
	}
	return paths
}

func fingerprintShellConfig(path string) shellConfigFingerprint {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return shellConfigFingerprint{known: true}
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
		return shellConfigFingerprint{}
	}
	file, err := os.Open(
		path,
	) // #nosec G304 -- Fixed startup-file candidates; read-only, bounded, never executed.
	if err != nil {
		return shellConfigFingerprint{}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return shellConfigFingerprint{}
	}
	return shellConfigFingerprint{digest: sha256.Sum256(data), exists: true, known: true}
}

func changedShellConfigs(before map[string]shellConfigFingerprint) []string {
	var changed []string
	for path, previous := range before {
		current := fingerprintShellConfig(path)
		if previous.known && current.known && previous != current {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}
