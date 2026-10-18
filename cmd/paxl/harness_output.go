package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/facade"
)

func validateHarnessOutputFormat(format string) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported output format %q; supported: text, json", format)
	}
	return nil
}

func renderHarnessOutput(out io.Writer, result any, format, action string) error {
	if err := validateHarnessOutputFormat(format); err != nil {
		return err
	}
	if format == "json" {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return fmt.Errorf("render harness JSON: %w", err)
		}
		return nil
	}
	var lines []string
	switch value := result.(type) {
	case *facade.HarnessInstallResult:
		lines = installResultLines(value)
	case *facade.HarnessInstallation:
		lines = installationResultLines(value)
		if action == "rollback" {
			lines[0] = "Rollback completed: " + value.Harness + " (" + string(value.Component) + ")"
		}
	case *facade.HarnessUpgradeResponse:
		lines = upgradeResultLines(value)
	default:
		return fmt.Errorf("unsupported harness output result")
	}
	if _, err := io.WriteString(out, strings.Join(lines, "\n")+"\n"); err != nil {
		return fmt.Errorf("render harness text: %w", err)
	}
	return nil
}

func installResultLines(result *facade.HarnessInstallResult) []string {
	title := "Package installation completed"
	switch result.State {
	case "planned":
		title = "Install plan"
	case "completed":
		title = "Official installer completed"
	}
	lines := []string{
		fmt.Sprintf("%s: %s (%s)", title, result.Harness, result.Component),
		"Route: " + string(result.Via) + " (" + result.Source + ")",
	}
	if result.Package != "" {
		lines = append(lines, "Package: "+result.Package)
	}
	lines = append(lines, "Requested version: "+result.Version)
	if result.ScriptURL != "" {
		lines = append(lines, "Installer: "+result.ScriptURL)
	}
	if result.State == "planned" {
		return append(
			lines,
			"Command: "+strings.Join(result.Command, " "),
			"Dry run; nothing was installed.",
		)
	}
	// Installers own activation and may change PATH. Do not label the requested
	// dist-tag as an observed version or infer a launcher from an unrelated PATH.
	lines = append(
		lines,
		"The installed version and launcher have not been independently verified.",
	)
	lines = append(lines, shellActivationLines(result.ChangedShellFiles)...)
	if result.Harness == "claude" || result.Harness == "pi" ||
		result.Component == facade.HarnessComponentACP {
		lines = append(
			lines,
			fmt.Sprintf(
				"Check: paxl harness inspect %s --component %s",
				result.Harness,
				result.Component,
			),
		)
	}
	if result.Harness == "codex" && result.Component == facade.HarnessComponentCLI {
		lines = append(lines, "Check: codex --version")
	}
	if result.Harness == "antigravity" {
		lines = append(lines, "Check: agy --version")
	}
	return lines
}

func shellActivationLines(paths []string) []string {
	if len(paths) == 0 {
		return []string{
			"No shell configuration changes were observed.",
			"If the command is not found, open a new terminal or follow the installer's PATH instructions above.",
		}
	}
	lines := []string{"Shell configuration changes detected during installation:"}
	for _, path := range paths {
		lines = append(lines, "  "+path)
	}
	lines = append(lines, "Open a new terminal to load the updated PATH.")
	if len(paths) != 1 {
		return lines
	}
	shell := ""
	switch filepath.Base(paths[0]) {
	case ".bashrc", ".bash_profile", ".bash_login":
		shell = "bash"
	case ".zshrc", ".zprofile", ".zshenv", ".zlogin":
		shell = "zsh"
	}
	info, err := os.Stat(paths[0])
	if shell != "" && err == nil && info.Mode().IsRegular() {
		// Quote paths as shell data, including spaces and embedded apostrophes.
		quoted := "'" + strings.ReplaceAll(paths[0], "'", "'\"'\"'") + "'"
		lines = append(
			lines,
			fmt.Sprintf(
				"If this terminal uses %s, you can reload it with: source %s",
				shell,
				quoted,
			),
		)
	}
	return lines
}

func installationResultLines(result *facade.HarnessInstallation) []string {
	lines := []string{
		fmt.Sprintf("%s (%s)", result.Harness, result.Component),
		"Version: " + result.Version,
		"Path: " + result.Path,
		"Source: " + result.Source,
	}
	if result.ResolvedPath != "" && result.ResolvedPath != result.Path {
		lines = append(lines, "Resolved path: "+result.ResolvedPath)
	}
	if result.Package != "" {
		lines = append(lines, "Package: "+result.Package)
	}
	return lines
}

func upgradeResultLines(result *facade.HarnessUpgradeResponse) []string {
	lines := installationResultLines(result.Installation)
	if result.Phase == "planned" {
		lines[0] = "Upgrade plan: " + lines[0]
		lines = append(
			lines,
			"Target version: "+result.TargetVersion,
			"Dry run; nothing was changed.",
		)
	} else {
		lines[0] = "Upgrade completed: " + lines[0]
	}
	if result.RollbackID != "" {
		lines = append(lines, "Rollback ID: "+result.RollbackID)
	}
	return lines
}
