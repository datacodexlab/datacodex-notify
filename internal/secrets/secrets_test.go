// SPDX-License-Identifier: GPL-3.0-only

package secrets

import (
	"errors"
	"testing"

	"github.com/danieljoos/wincred"
)

func TestSecretsRoundTrip(t *testing.T) {
	store := NewWinCredStore()
	testTarget := "datacodex-notify/test-secret"
	testValue := "secret-value-12345"

	// Ensure cleanup before and after
	_ = store.Delete(testTarget)
	defer store.Delete(testTarget)

	err := store.Set(testTarget, testValue)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := store.Get(testTarget)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != testValue {
		t.Errorf("expected %s, got %s", testValue, val)
	}

	err = store.Delete(testTarget)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(testTarget)
	if err == nil {
		t.Errorf("expected error after delete, got nil")
	}
}

func TestGenerateSecureToken(t *testing.T) {
	token, err := GenerateSecureToken(32)
	if err != nil {
		t.Fatalf("GenerateSecureToken failed: %v", err)
	}
	if len(token) < 40 {
		t.Errorf("token too short: %s", token)
	}
}

func TestGetMissingSecretIsNotFound(t *testing.T) {
	store := NewWinCredStore()
	target := "datacodex-notify/test-missing-secret"
	_ = store.Delete(target)

	if _, err := store.Get(target); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a missing secret, got %v", err)
	}
}

func TestClassifyCredError(t *testing.T) {
	if err := classifyCredError("t", wincred.ErrElementNotFound); !errors.Is(err, ErrNotFound) {
		t.Errorf("not-found error classified as %v", err)
	}

	other := errors.New("access denied")
	err := classifyCredError("t", other)
	if errors.Is(err, ErrNotFound) {
		t.Error("a non-missing error must not be reported as not found")
	}
	if !errors.Is(err, other) {
		t.Errorf("original error not wrapped: %v", err)
	}
}
