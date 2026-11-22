package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/grmrgecko/karakuri/internal/resources"
)

// TestAgentIdentityDirFollowsConfigLocation locks where the agent's key and
// certificate land when nothing names a directory. The answer follows the
// config file so state stays with the rest of the install, and it has to be
// stable: an agent that resolves a different directory after an upgrade looks
// unenrolled and re-enrolls as a second agent.
func TestAgentIdentityDirFollowsConfigLocation(t *testing.T) {
	const binDir = "/opt/karakuri"
	const userDir = "/home/op/.config/karakuri"

	tests := []struct {
		name       string
		configPath string
		userDir    string
		want       string
	}{
		{
			name:       "config beside the binary keeps state beside the binary",
			configPath: filepath.Join(binDir, "etc", AgentConfigName),
			userDir:    userDir,
			want:       filepath.Join(binDir, "var", agentIdentityDirName),
		},
		{
			name:       "system config keeps state in the system data directory",
			configPath: filepath.Join(resources.SystemConfigDir, AgentConfigName),
			userDir:    userDir,
			want:       filepath.Join(resources.SystemDataDir, agentIdentityDirName),
		},
		{
			name:       "any other location keeps state beside the config file",
			configPath: filepath.Join(userDir, AgentConfigName),
			userDir:    userDir,
			want:       filepath.Join(userDir, agentIdentityDirName),
		},
		{
			name:       "no config file falls back to the user config directory",
			configPath: "",
			userDir:    userDir,
			want:       filepath.Join(userDir, agentIdentityDirName),
		},
		{
			name:       "no config file and no home falls back beside the binary",
			configPath: "",
			userDir:    "",
			want:       filepath.Join(binDir, "var", agentIdentityDirName),
		},
	}
	for _, tc := range tests {
		assert.Equalf(t, tc.want, agentIdentityDirFor(tc.configPath, binDir, tc.userDir), "%s", tc.name)
	}
}
