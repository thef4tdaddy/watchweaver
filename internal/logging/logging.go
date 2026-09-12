package logging

import (
	"log"
	"sync/atomic"
)

var debug atomic.Bool

func SetDebug(enabled bool) { debug.Store(enabled) }
func DebugEnabled() bool    { return debug.Load() }

func Debugf(format string, args ...any) {
	if debug.Load() {
		log.Printf("DEBUG "+format, args...)
	}
}
