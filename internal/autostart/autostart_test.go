// SPDX-License-Identifier: GPL-3.0-only

package autostart

import (
	"errors"
	"os"
	"strconv"
	"testing"
)

type fakeRegistry struct {
	values  map[string]string
	failGet error
	failSet error
	failDel error
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{values: map[string]string{}} }

func (f *fakeRegistry) Get(name string) (string, bool, error) {
	if f.failGet != nil {
		return "", false, f.failGet
	}
	v, ok := f.values[name]
	return v, ok, nil
}

func (f *fakeRegistry) Set(name, value string) error {
	if f.failSet != nil {
		return f.failSet
	}
	f.values[name] = value
	return nil
}

func (f *fakeRegistry) Delete(name string) error {
	if f.failDel != nil {
		return f.failDel
	}
	delete(f.values, name)
	return nil
}

func TestEnableWritesTheQuotedPathAndDisableRemovesIt(t *testing.T) {
	reg := newFakeRegistry()
	m := New(reg, ValueName, `C:\Program Files\Notify\datacodex-notify.exe`)

	if on, err := m.Enabled(); err != nil || on {
		t.Fatalf("initially: on=%v err=%v", on, err)
	}
	if err := m.Set(true); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if got := reg.values[ValueName]; got != `"C:\Program Files\Notify\datacodex-notify.exe"` {
		t.Errorf("stored command = %s", got)
	}
	if on, _ := m.Enabled(); !on {
		t.Error("expected enabled")
	}

	if err := m.Set(false); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if _, present := reg.values[ValueName]; present {
		t.Error("the entry must be removed")
	}
	if on, _ := m.Enabled(); on {
		t.Error("expected disabled")
	}
	if err := m.Set(false); err != nil {
		t.Errorf("disabling twice must be harmless: %v", err)
	}
}

func TestEnableRewritesAStaleEntry(t *testing.T) {
	reg := newFakeRegistry()
	reg.values[ValueName] = `"D:\old\datacodex-notify.exe"`
	m := New(reg, ValueName, `C:\new\datacodex-notify.exe`)

	if on, _ := m.Enabled(); !on {
		t.Error("an entry that points elsewhere still counts as enabled")
	}
	if err := m.Set(true); err != nil {
		t.Fatal(err)
	}
	if reg.values[ValueName] != `"C:\new\datacodex-notify.exe"` {
		t.Errorf("stored command = %s", reg.values[ValueName])
	}
}

func TestAlreadyQuotedPathsAreNotQuotedTwice(t *testing.T) {
	reg := newFakeRegistry()
	m := New(reg, ValueName, `"C:\x\app.exe"`)
	_ = m.Set(true)
	if reg.values[ValueName] != `"C:\x\app.exe"` {
		t.Errorf("stored command = %s", reg.values[ValueName])
	}
}

func TestRegistryErrorsAreReported(t *testing.T) {
	boom := errors.New("access denied")
	reg := newFakeRegistry()
	m := New(reg, ValueName, `C:\app.exe`)

	reg.failGet = boom
	if _, err := m.Enabled(); !errors.Is(err, boom) {
		t.Errorf("Enabled: %v", err)
	}
	reg.failGet = nil

	reg.failSet = boom
	if err := m.Set(true); !errors.Is(err, boom) {
		t.Errorf("enable: %v", err)
	}
	reg.failSet = nil

	reg.failDel = boom
	if err := m.Set(false); !errors.Is(err, boom) {
		t.Errorf("disable: %v", err)
	}
}

// TestRealRunKeyRoundTrip writes to the real per-user Run key, so it only runs
// when asked: set DATACODEX_REGISTRY_TEST=1. It uses a value name of its own and removes it.
func TestRealRunKeyRoundTrip(t *testing.T) {
	if os.Getenv("DATACODEX_REGISTRY_TEST") != "1" {
		t.Skip("set DATACODEX_REGISTRY_TEST=1 to run the real registry round trip")
	}

	name := "DatacodexNotifyTest-" + strconv.Itoa(os.Getpid())
	m := New(RunKey{}, name, `C:\Windows\System32\notepad.exe`)
	t.Cleanup(func() { _ = RunKey{}.Delete(name) })

	if on, err := m.Enabled(); err != nil || on {
		t.Fatalf("test value should not exist yet: on=%v err=%v", on, err)
	}
	if err := m.Set(true); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	value, found, err := RunKey{}.Get(name)
	if err != nil || !found || value != `"C:\Windows\System32\notepad.exe"` {
		t.Fatalf("stored value = %q found=%v err=%v", value, found, err)
	}
	if err := m.Set(false); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if on, _ := m.Enabled(); on {
		t.Error("the test value was not removed")
	}
}
