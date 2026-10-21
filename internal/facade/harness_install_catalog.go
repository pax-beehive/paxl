package facade

import (
	"fmt"
	"runtime"
	"strings"
)

type HarnessInstallVia string

const (
	HarnessInstallViaUnknown  HarnessInstallVia = ""
	HarnessInstallViaOfficial HarnessInstallVia = "official"
	HarnessInstallViaNPM      HarnessInstallVia = "npm"
)

func ParseHarnessInstallVia(raw string) (HarnessInstallVia, error) {
	switch HarnessInstallVia(raw) {
	case HarnessInstallViaUnknown, HarnessInstallViaOfficial:
		return HarnessInstallViaOfficial, nil
	case HarnessInstallViaNPM:
		return HarnessInstallViaNPM, nil
	default:
		return HarnessInstallViaUnknown, fmt.Errorf(
			"unsupported installation method %q; supported: official (default), npm (Pi only)",
			raw,
		)
	}
}

type officialHarnessInstaller struct {
	harness           string
	scriptURL         string
	shell             string
	npmPackage        string
	args              []string
	versionFlag       string
	versionPositional bool
}

// Only publisher-owned URLs and packages belong here. "official" denotes the
// publisher's installation route, not an assertion that every route is a script.
var officialHarnessInstallers = []officialHarnessInstaller{
	{
		harness:           "claude",
		scriptURL:         "https://claude.ai/install.sh",
		shell:             "bash",
		versionPositional: true,
	},
	{
		harness:     "codex",
		scriptURL:   "https://chatgpt.com/codex/install.sh",
		shell:       "sh",
		versionFlag: "--release",
	},
	{harness: "pi", scriptURL: "https://pi.dev/install.sh", shell: "sh"},
	{harness: "gemini", npmPackage: "@google/gemini-cli"},
	{harness: "antigravity", scriptURL: "https://antigravity.google/cli/install.sh", shell: "bash"},
	{
		harness:     "kimi",
		scriptURL:   "https://code.kimi.com/kimi-code/install.sh",
		shell:       "bash",
		versionFlag: "--version",
	},
	{
		harness:     "opencode",
		scriptURL:   "https://opencode.ai/install",
		shell:       "bash",
		versionFlag: "--version",
	},
	{
		harness: "hermes",
	},
	{
		harness:     "openclaw",
		scriptURL:   "https://openclaw.ai/install.sh",
		shell:       "bash",
		args:        []string{"--no-onboard"},
		versionFlag: "--version",
	},
	{harness: "kiro", scriptURL: "https://cli.kiro.dev/install", shell: "bash"},
	{harness: "dsh", npmPackage: "@deepseek-ai/dsh"},
	{
		harness:           "dscode",
		scriptURL:         "https://raw.githubusercontent.com/qiz029/dscode/main/install.sh",
		shell:             "sh",
		versionPositional: true,
	},
}

func SupportedHarnessInstallNames() string {
	names := make([]string, 0, len(officialHarnessInstallers))
	for _, entry := range officialHarnessInstallers {
		names = append(names, entry.harness)
	}
	return strings.Join(names, ", ")
}

func planHarnessInstall(req *HarnessInstallRequest) (*HarnessInstallResult, error) {
	via, err := ParseHarnessInstallVia(string(req.Via))
	if err != nil {
		return nil, err
	}
	harness := strings.ToLower(strings.TrimSpace(req.Harness))
	if harness == "claude-code" {
		harness = "claude"
	}
	var definition *officialHarnessInstaller
	for i := range officialHarnessInstallers {
		if officialHarnessInstallers[i].harness == harness {
			definition = &officialHarnessInstallers[i]
			break
		}
	}
	if definition == nil {
		return nil, fmt.Errorf(
			"unsupported install harness %q; supported harnesses: %s\nExamples:\n  paxl harness install claude\n  paxl harness install pi --component acp\nRun paxl harness install -h for installation options",
			req.Harness,
			SupportedHarnessInstallNames(),
		)
	}
	component := req.Component
	if component == HarnessComponentUnknown {
		component = HarnessComponentCLI
	}
	if component != HarnessComponentCLI && component != HarnessComponentACP {
		return nil, fmt.Errorf(
			"unsupported installation component %q; supported: cli, acp",
			component,
		)
	}
	version := req.Version
	if version == "" {
		version = "latest"
	}
	if version != "latest" && !harnessVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("version must be latest or an exact semantic version")
	}
	result := &HarnessInstallResult{
		Harness:   harness,
		Component: component,
		Via:       via,
		Version:   version,
		State:     "planned",
	}
	if component == HarnessComponentACP {
		name, _, err := harnessPackageName(
			&HarnessInspectRequest{Harness: harness, Component: component},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"separate ACP package installation supports claude, codex, pi; %s uses its native ACP entry or a separately managed adapter",
				harness,
			)
		}
		if via != HarnessInstallViaOfficial {
			return nil, fmt.Errorf("ACP installation supports --via official")
		}
		return npmInstallPlan(result, name), nil
	}
	if via == HarnessInstallViaNPM {
		if harness != "pi" {
			return nil, fmt.Errorf(
				"--via npm currently supports pi; use --via official for %s",
				harness,
			)
		}
		return npmInstallPlan(result, "@earendil-works/pi-coding-agent"), nil
	}
	return officialInstallPlan(result, definition, runtime.GOOS)
}

func npmInstallPlan(result *HarnessInstallResult, name string) *HarnessInstallResult {
	result.Package = name
	result.Source = "npm"
	result.Command = []string{"npm", "install", "-g", name + "@" + result.Version}
	return result
}

func officialInstallPlan(
	result *HarnessInstallResult,
	entry *officialHarnessInstaller,
	platform string,
) (*HarnessInstallResult, error) {
	if result.Harness == "hermes" {
		if result.Version != "latest" {
			return nil, fmt.Errorf(
				"hermes prebuilt installation selects the newest available platform build; omit --version",
			)
		}
		result.Source = "prebuilt"
		result.Package = "https://hermes-assets.nousresearch.com"
		result.Command = []string{"<download-and-verify-official-prebuilt>"}
		return result, nil
	}
	if entry.npmPackage != "" {
		return npmInstallPlan(result, entry.npmPackage), nil
	}
	if platform != "darwin" && platform != "linux" {
		return nil, fmt.Errorf(
			"official script installation for %s currently supports macOS/Linux (including WSL); platform %s is not supported",
			result.Harness,
			platform,
		)
	}
	args := append([]string(nil), entry.args...)
	if result.Version != "latest" {
		switch {
		case entry.versionPositional:
			args = append(args, result.Version)
		case entry.versionFlag != "":
			args = append(args, entry.versionFlag, result.Version)
		default:
			return nil, fmt.Errorf(
				"official %s installer does not expose a verified exact-version option; omit --version to use its default release",
				result.Harness,
			)
		}
	} else if result.Harness == "claude" {
		args = append(args, "latest")
	}
	result.Source = "script"
	result.ScriptURL = entry.scriptURL
	result.Command = append([]string{entry.shell, "<downloaded-official-installer>"}, args...)
	return result, nil
}
