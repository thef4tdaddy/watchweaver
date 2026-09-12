package logging

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestDebugfHonorsConfiguredLevel(t *testing.T) {
	old := log.Writer()
	defer log.SetOutput(old)
	defer SetDebug(false)
	var output bytes.Buffer
	log.SetOutput(&output)
	SetDebug(false)
	Debugf("hidden %s", "secret")
	if output.Len() != 0 {
		t.Fatalf("debug message emitted while disabled: %q", output.String())
	}
	SetDebug(true)
	Debugf("event_type=%s", "completed")
	if !strings.Contains(output.String(), "DEBUG event_type=completed") {
		t.Fatalf("missing debug message: %q", output.String())
	}
}
