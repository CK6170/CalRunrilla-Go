//go:build !windows

package ui

import (
	"sync"

	"github.com/eiannone/keyboard"
)

// Non-windows implementation uses github.com/eiannone/keyboard (works on Linux/macOS).
// Keep behavior consistent with the windows build.
var (
	keyChNW     chan rune
	startOnceNW sync.Once
)

// StartKeyEvents returns a channel that emits single-key runes read without Enter.
func StartKeyEvents() chan rune {
	startOnceNW.Do(func() {
		keyChNW = make(chan rune, 64)
		if err := keyboard.Open(); err != nil {
			// Keyboard not available; keep a buffered channel that will never emit.
			return
		}
		go func() {
			defer keyboard.Close()
			for {
				char, key, err := keyboard.GetKey()
				if err != nil {
					close(keyChNW)
					return
				}
				if key == 0 {
					select {
					case keyChNW <- char:
					default:
					}
				} else if key == keyboard.KeyEsc {
					select {
					case keyChNW <- 27:
					default:
					}
				}
			}
		}()
	})
	if keyChNW == nil {
		keyChNW = make(chan rune, 64)
	}
	return keyChNW
}

// DrainKeys consumes any immediately available keys to avoid accidental triggers.
func DrainKeys() {
	ch := StartKeyEvents()
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

