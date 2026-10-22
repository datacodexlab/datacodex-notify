// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultPort = 48700
	MaxPort     = 48709
	AppName     = "DatacodexNotify"
)

type Config struct {
	DataDir     string
	AppDBPath   string
	WhatsAppDB  string
	RuntimePath string
	LogsDir     string
	TmpDir      string
	EdgeProfile string
}

type RuntimeInfo struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
}

func LoadConfig() (*Config, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home dir: %w", err)
		}
		localAppData = filepath.Join(home, "AppData", "Local")
	}

	dataDir := filepath.Join(localAppData, AppName)
	return NewConfigWithDir(dataDir)
}

func NewConfigWithDir(dataDir string) (*Config, error) {
	cfg := &Config{
		DataDir:     dataDir,
		AppDBPath:   filepath.Join(dataDir, "app.db"),
		WhatsAppDB:  filepath.Join(dataDir, "whatsapp.db"),
		RuntimePath: filepath.Join(dataDir, "runtime.json"),
		LogsDir:     filepath.Join(dataDir, "logs"),
		TmpDir:      filepath.Join(dataDir, "tmp"),
		EdgeProfile: filepath.Join(dataDir, "edge-profile"),
	}

	for _, dir := range []string{cfg.DataDir, cfg.LogsDir, cfg.TmpDir, cfg.EdgeProfile} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return cfg, nil
}

func FindFreePort(startPort, maxPort int) (int, error) {
	for port := startPort; port <= maxPort; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			_ = l.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port available in range %d-%d", startPort, maxPort)
}

func (c *Config) WriteRuntime(port int, version string) (*RuntimeInfo, error) {
	info := &RuntimeInfo{
		PID:       os.Getpid(),
		Port:      port,
		StartedAt: time.Now().UTC(),
		Version:   version,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runtime info: %w", err)
	}

	if err := os.WriteFile(c.RuntimePath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write runtime file: %w", err)
	}
	return info, nil
}

func (c *Config) ReadRuntime() (*RuntimeInfo, error) {
	data, err := os.ReadFile(c.RuntimePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime file: %w", err)
	}

	var info RuntimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal runtime info: %w", err)
	}
	return &info, nil
}

// RemoveRuntime deletes runtime.json so the CLI reports "not running" after a clean exit.
func (c *Config) RemoveRuntime() error {
	if err := os.Remove(c.RuntimePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove runtime file: %w", err)
	}
	return nil
}
