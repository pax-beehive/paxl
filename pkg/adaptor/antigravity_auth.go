package adaptor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"golang.org/x/term"
)

func validateAntigravityAuth(req *NativeAuthRequest) error {
	if req.Provider != "" || req.Profile != "" {
		return fmt.Errorf(
			"antigravity uses its native account; --provider and --profile are not supported",
		)
	}
	if req.SecretStdin {
		return fmt.Errorf(
			"antigravity API keys use GEMINI_API_KEY in the launching environment; --secret-stdin cannot persist that environment",
		)
	}
	if req.Operation == model.HarnessAuthLogin {
		_, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness)
		return err
	}
	if req.Method != "" {
		return fmt.Errorf("method is only valid for login")
	}
	// Login was validated above; other operations have no method.
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthStatus, model.HarnessAuthLogout, model.HarnessAuthMethods:
		return nil
	}
	return fmt.Errorf("unsupported Antigravity authentication operation")
}

// RunAntigravity delegates account interactions to AGY's own foreground TUI.
// AGY currently has no standalone account API; closing its UI is not proof of login.
func (a *NativeHarnessAuth) RunAntigravity(
	ctx context.Context,
	req *NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication request is required")
	}
	if err := validateAntigravityAuth(req); err != nil {
		return nil, err
	}
	result := &model.HarnessAuthResult{
		Harness: model.AgentNameAntigravity,
		State:   model.HarnessAuthStateUnknown,
	}
	if req.Operation == model.HarnessAuthMethods {
		result.State = model.HarnessAuthAvailable
		result.Providers = []model.HarnessAuthProvider{
			{
				ID: "google",
				Methods: []model.HarnessAuthMethod{
					model.HarnessAuthInteractive,
					model.HarnessAuthAPIKey,
					model.HarnessAuthADC,
				},
			},
		}
		result.Message = "interactive opens AGY's native account UI. api-key/adc select existing environment credentials; Pax does not store those secrets."
		return result, nil
	}
	if req.Operation == model.HarnessAuthStatus {
		if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("AGY_ADC_AUTH") == "true" {
			result.State = model.HarnessAuthConfigured
		}
		result.Message = "Environment credential presence only. AGY's native account keyring and model access cannot be queried through its CLI; inspect the native UI."
		return result, nil
	}
	if req.Operation == model.HarnessAuthLogin &&
		(req.Method == model.HarnessAuthAPIKey || req.Method == model.HarnessAuthADC) {
		if req.Method == model.HarnessAuthAPIKey {
			if os.Getenv("GEMINI_API_KEY") == "" {
				return nil, fmt.Errorf(
					"set GEMINI_API_KEY in the environment used to launch AGY, then retry",
				)
			}
			if err := selectAntigravityAPIKey(); err != nil {
				return nil, err
			}
		} else if os.Getenv("AGY_ADC_AUTH") != "true" {
			return nil, fmt.Errorf("set AGY_ADC_AUTH=true and configure native ADC, then retry")
		}
		result.State = model.HarnessAuthConfigured
		result.Method = req.Method
		result.Message = "Selected existing environment authentication. Keep these variables in the environment that launches AGY; credentials and model access are not verified."
		return result, nil
	}
	// Do not send a slash command as a model prompt or assume undocumented auth flags.
	file, ok := req.Stdin.(*os.File)
	if !ok {
		return nil, fmt.Errorf(
			"antigravity account authentication requires a terminal; run paxl harness auth %s --harness antigravity interactively",
			req.Operation,
		)
	}
	if !term.IsTerminal(int(file.Fd())) {
		return nil, fmt.Errorf("antigravity account authentication requires a terminal")
	}
	if req.Operation == model.HarnessAuthLogout {
		_, _ = fmt.Fprintln(
			req.Stderr,
			"In AGY, run /logout, then exit. Environment/API-key/ADC credentials must be removed at their source.",
		)
	} else {
		_, _ = fmt.Fprintln(req.Stderr, "Complete login in AGY, then exit to return to paxl.")
	}
	cmd := exec.CommandContext(
		ctx,
		authExecutable(model.AgentNameAntigravity),
	) // #nosec G204 -- Selected native executable; no model prompt or shell.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	prepareNativeAuth(cmd)
	defer stopNativeAuth(cmd)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run native Antigravity account UI: %w", err)
	}
	result.Message = "Native AGY UI closed. Account completion is not independently verified."
	return result, nil
}

func selectAntigravityAPIKey() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	data, err := os.ReadFile(
		path,
	) // #nosec G304 -- Official native settings under the caller's home.
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read AGY settings: %w", err)
	}
	settings := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
			return fmt.Errorf("AGY settings must be a JSON object")
		}
	}
	settings["modelProvider"] = "gemini"
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode AGY settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create AGY settings directory: %w", err)
	}
	// Preserve a caller's symlink rather than replacing it with a regular file.
	if target, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = target
	} else if !os.IsNotExist(resolveErr) {
		return fmt.Errorf("resolve AGY settings: %w", resolveErr)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".paxl-settings-*")
	if err != nil {
		return fmt.Errorf("stage AGY settings: %w", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err = temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write AGY settings: %w", err)
	}
	if err = temp.Close(); err != nil {
		return fmt.Errorf("close AGY settings: %w", err)
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replace AGY settings: %w", err)
	}
	return nil
}
