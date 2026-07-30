package sandboxagent

import (
	"fmt"
	"os"
	"strings"
)

const (
	EnvSandboxID = "CELLAR_SANDBOX_ID"
	EnvAgentSock = "CELLAR_AGENT_SOCK"
	EnvTokenFile = "CELLAR_AGENT_TOKEN_FILE"

	DefaultSock      = "/run/cellar/agent.sock"
	DefaultTokenFile = "/run/cellar/agent.token"
)

// Config is loaded from the environment inside the sandbox.
type Config struct {
	SandboxID string
	SockPath  string
	Token     string
}

// LoadConfig reads agent configuration from env and the token file.
func LoadConfig() (Config, error) {
	cfg := Config{
		SandboxID: strings.TrimSpace(os.Getenv(EnvSandboxID)),
		SockPath:  strings.TrimSpace(os.Getenv(EnvAgentSock)),
	}
	if cfg.SockPath == "" {
		cfg.SockPath = DefaultSock
	}
	tokenFile := strings.TrimSpace(os.Getenv(EnvTokenFile))
	if tokenFile == "" {
		tokenFile = DefaultTokenFile
	}
	if cfg.SandboxID == "" {
		return Config{}, fmt.Errorf("%s is required", EnvSandboxID)
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return Config{}, fmt.Errorf("read token file %s: %w", tokenFile, err)
	}
	cfg.Token = strings.TrimSpace(string(raw))
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("token file %s is empty", tokenFile)
	}
	return cfg, nil
}
