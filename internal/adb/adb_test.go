package adb

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestParseDevices(t *testing.T) {
	out := `List of devices attached
emulator-5554	device
192.168.1.5:5555	offline
0123456789ABCDEF	unauthorized
FA6970301234	no permissions (udev rules missing)

`
	got := ParseDevices(out)
	want := []Device{
		{Serial: "emulator-5554", State: "device"},
		{Serial: "192.168.1.5:5555", State: "offline"},
		{Serial: "0123456789ABCDEF", State: "unauthorized"},
		{Serial: "FA6970301234", State: "no permissions (udev rules missing)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseDevices mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseDevicesSkipsDaemonAndEmpty(t *testing.T) {
	out := `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
emulator-5554	device
`
	got := ParseDevices(out)
	if len(got) != 1 || got[0].Serial != "emulator-5554" {
		t.Fatalf("expected single emulator device, got %#v", got)
	}
}

func TestDeviceOnline(t *testing.T) {
	if !(Device{State: "device"}).Online() {
		t.Error("state=device should be online")
	}
	for _, s := range []string{"offline", "unauthorized", "no permissions", ""} {
		if (Device{State: s}).Online() {
			t.Errorf("state=%q should not be online", s)
		}
	}
}

func TestShellRespectsWorkerPoolCancellation(t *testing.T) {
	// With a pool of size 1 already saturated, a cancelled context must make
	// Shell return promptly instead of blocking forever.
	c := New("adb", time.Second, 1)
	c.sem <- struct{}{} // saturate the pool

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Shell(ctx, "serial", "echo hi")
	if err == nil {
		t.Fatal("expected error when context is cancelled and pool is full")
	}
}
