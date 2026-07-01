package service

import "testing"

func TestIsControlCommand(t *testing.T) {
	for _, c := range ControlCommands {
		if !IsControlCommand(c) {
			t.Errorf("%q should be a valid control command", c)
		}
	}
	for _, c := range []string{"", "run", "reload", "foo"} {
		if IsControlCommand(c) {
			t.Errorf("%q should not be a valid control command", c)
		}
	}
}

func TestControlRejectsUnknown(t *testing.T) {
	// Control must reject unknown commands before touching the OS service manager.
	if err := Control(nil, "bogus"); err == nil {
		t.Error("expected error for unknown control command")
	}
}
