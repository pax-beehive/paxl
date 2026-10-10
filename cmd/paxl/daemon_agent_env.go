package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
)

var daemonEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseDaemonAgentEnv(entries []string) (map[string]string, error) {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if !found || !daemonEnvName.MatchString(key) || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf(
				"agent environment: --env requires KEY=VALUE with a valid environment name and no NUL bytes",
			)
		}
		env[key] = value
	}
	return env, nil
}

func parseDaemonAgentEnvUpdate(cmd *cli.Command) (*map[string]string, error) {
	if cmd.IsSet("env") && cmd.Bool("clear-env") {
		return nil, fmt.Errorf("agent update: --env and --clear-env are mutually exclusive")
	}
	if !cmd.IsSet("env") && !cmd.Bool("clear-env") {
		return nil, nil //nolint:nilnil // A nil patch preserves the saved environment.
	}
	env, err := parseDaemonAgentEnv(cmd.StringSlice("env"))
	if err != nil {
		return nil, err
	}
	return &env, nil
}
