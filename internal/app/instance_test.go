// SPDX-License-Identifier: GPL-3.0-only

package app

import "testing"

func TestSecondInstanceIsDetected(t *testing.T) {
	name := `Local\DatacodexNotifyTest-` + t.Name()

	first, running, err := acquireInstance(name)
	if err != nil || running || first == nil {
		t.Fatalf("first acquire: lock=%v running=%v err=%v", first, running, err)
	}

	second, running, err := acquireInstance(name)
	if err != nil || !running || second != nil {
		t.Fatalf("second acquire should report an instance already running: lock=%v running=%v err=%v", second, running, err)
	}

	first.release()
	third, running, err := acquireInstance(name)
	if err != nil || running || third == nil {
		t.Fatalf("acquire after release: lock=%v running=%v err=%v", third, running, err)
	}
	third.release()
}
