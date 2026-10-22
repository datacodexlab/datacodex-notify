// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"os"
	"testing"
)

func TestConfigRuntimeRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg, err := NewConfigWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewConfigWithDir failed: %v", err)
	}

	port, err := FindFreePort(DefaultPort, MaxPort)
	if err != nil {
		t.Fatalf("FindFreePort failed: %v", err)
	}

	info, err := cfg.WriteRuntime(port, "0.1.0")
	if err != nil {
		t.Fatalf("WriteRuntime failed: %v", err)
	}

	readInfo, err := cfg.ReadRuntime()
	if err != nil {
		t.Fatalf("ReadRuntime failed: %v", err)
	}

	if readInfo.Port != port {
		t.Errorf("expected port %d, got %d", port, readInfo.Port)
	}
	if readInfo.PID != info.PID {
		t.Errorf("expected PID %d, got %d", info.PID, readInfo.PID)
	}
	if readInfo.Version != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", readInfo.Version)
	}

	if err := cfg.RemoveRuntime(); err != nil {
		t.Fatalf("RemoveRuntime failed: %v", err)
	}
	if _, err := cfg.ReadRuntime(); err == nil {
		t.Errorf("expected error after RemoveRuntime, got nil")
	}
}
