package facade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const hermesSourceInstaller = "https://hermes-agent.nousresearch.com/install.sh"

func (f *HarnessInstallFacade) installHermesSource(
	ctx context.Context,
	result *HarnessInstallResult,
	option *Option,
) (*HarnessInstallResult, error) {
	if option.Stderr != nil {
		_, _ = fmt.Fprintln(
			option.Stderr,
			"No compatible prebuilt Hermes package is available. Checking source-build prerequisites for darwin/x64.",
		)
	}
	if err := checkHermesBuildPrerequisites(ctx); err != nil {
		return nil, err
	}
	source := *result
	source.Source = "script"
	source.ScriptURL = hermesSourceInstaller
	source.Package = ""
	source.Command = []string{"bash", "<downloaded-official-installer>", "--skip-setup"}
	if option.Stderr != nil {
		_, _ = fmt.Fprintln(
			option.Stderr,
			"Build prerequisites passed. Running the official Hermes source installer.",
		)
	}
	return f.runHarnessInstallPlan(ctx, &source, option)
}

func checkHermesBuildPrerequisites(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var missing []string
	for _, name := range []string{"bash", "git", "curl", "tar"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, "missing "+name)
		}
	}
	if _, err := exec.LookPath("shasum"); err != nil {
		if _, otherErr := exec.LookPath("sha256sum"); otherErr != nil {
			missing = append(missing, "missing shasum or sha256sum")
		}
	}
	for _, name := range []string{"rustc", "cargo"} {
		if err := checkHermesRustVersion(ctx, name); err != nil {
			missing = append(missing, err.Error())
		}
	}
	if _, err := hermesBuildProbe(ctx, "xcrun", "--find", "clang"); err != nil {
		missing = append(
			missing,
			"Apple command-line tools are unavailable; run xcode-select --install",
		)
	}
	if err := checkHermesOpenSSL(ctx); err != nil {
		missing = append(missing, err.Error())
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"hermes source-build prerequisites failed:\n- %s",
			strings.Join(missing, "\n- "),
		)
	}
	return nil
}

var hermesRustVersionPattern = regexp.MustCompile(
	`^(?:rustc|cargo) ([0-9]+)\.([0-9]+)\.[0-9]+(?:\s|$)`,
)

func checkHermesRustVersion(ctx context.Context, name string) error {
	output, err := hermesBuildProbe(ctx, name, "--version")
	if err != nil {
		return fmt.Errorf(
			"%s is unavailable; install Rust >= 1.83.0 and ensure rustc/cargo are on PATH",
			name,
		)
	}
	match := hermesRustVersionPattern.FindStringSubmatch(output)
	if len(match) != 3 {
		return fmt.Errorf(
			"could not verify %s version; Hermes source builds require Rust >= 1.83.0",
			name,
		)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major < 1 || (major == 1 && minor < 83) {
		return fmt.Errorf(
			"%s reports %s; Hermes source builds require Rust >= 1.83.0; run rustup update stable and check %s --version",
			name,
			strings.TrimSpace(output),
			name,
		)
	}
	return nil
}

func hermesBuildProbe(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(
		ctx,
		name,
		args...) // #nosec G204 -- Callers pass fixed build-tool names and flags; no shell.
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("check %s: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func checkHermesOpenSSL(ctx context.Context) error {
	if root := os.Getenv("OPENSSL_DIR"); root != "" {
		if info, err := os.Stat( // #nosec G703 -- OPENSSL_DIR intentionally selects the user-owned native build dependency; metadata only.
			filepath.Join(root, "include", "openssl", "ssl.h"),
		); err == nil &&
			info.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf(
			"OPENSSL_DIR does not contain include/openssl/ssl.h; point it at an OpenSSL development installation",
		)
	}
	if version, err := hermesBuildProbe(
		ctx,
		"pkg-config",
		"--modversion",
		"openssl",
	); err == nil &&
		strings.HasPrefix(version, "3.") {
		return nil
	}
	if root, err := hermesBuildProbe(ctx, "brew", "--prefix", "openssl@3"); err == nil {
		if info, err := os.Stat(
			filepath.Join(root, "include", "openssl", "ssl.h"),
		); err == nil &&
			info.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf(
		"OpenSSL 3 development files were not found; install openssl@3 and pkg-config, or set OPENSSL_DIR",
	)
}
