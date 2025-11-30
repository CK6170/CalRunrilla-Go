//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows virtual key codes we care about
const (
	vkC   = 0x43
	vkEsc = 0x1B
	vkY   = 0x59
	vkN   = 0x4E
	vkR   = 0x52
	vkF   = 0x46
	vkS   = 0x53
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle     = kernel32.NewProc("GetStdHandle")
	procReadConsoleInput = kernel32.NewProc("ReadConsoleInputW")
	procGetConsoleMode   = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode   = kernel32.NewProc("SetConsoleMode")
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

const (
	stdInputHandle  = ^uintptr(10) + 1
	keyEvent        = 0x0001
	enableLineInput = 0x0002
	enableEchoInput = 0x0004
)

// inputRecord mirrors Windows INPUT_RECORD
type inputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

// keyEventRecord mirrors KEY_EVENT_RECORD
type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// StartKeyEvents returns a channel producing runes for edge key presses (C, ESC)
func consoleSetRaw() (orig uint32, err error) {
	handle, _, _ := procGetStdHandle.Call(stdInputHandle)
	var mode uint32
	if r, _, e := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return 0, e
	}
	orig = mode
	newMode := mode &^ (enableLineInput | enableEchoInput)
	procSetConsoleMode.Call(handle, uintptr(newMode))
	return orig, nil
}

func consoleRestore(mode uint32) {
	handle, _, _ := procGetStdHandle.Call(stdInputHandle)
	procSetConsoleMode.Call(handle, uintptr(mode))
}

var (
	keyChan   chan rune
	startOnce sync.Once
)

func StartKeyEvents() <-chan rune {
	startOnce.Do(func() {
		keyChan = make(chan rune, 32)
		go runKeyReader()
	})
	return keyChan
}

// DrainKeys empties any buffered key events to avoid stale presses affecting the next phase
func DrainKeys() {
	if keyChan == nil {
		return
	}
	for {
		select {
		case <-keyChan:
			// discard
		default:
			return
		}
	}
}

func runKeyReader() {
	handle, _, _ := procGetStdHandle.Call(stdInputHandle)
	// Attempt raw mode (ignore error, fallback to normal if fails)
	var orig uint32
	if m, err := consoleSetRaw(); err == nil {
		orig = m
	}
	defer func() {
		if orig != 0 {
			consoleRestore(orig)
		}
	}()
	var rec inputRecord
	var read uint32
	// Track last state to implement edge detection for relevant keys
	prevDown := map[uint16]bool{
		vkC:   false,
		vkEsc: false,
		vkY:   false,
		vkN:   false,
		vkR:   false,
		vkF:   false,
		vkS:   false,
	}
	// Also track async state polling to improve responsiveness in shells that buffer input
	prevAsync := map[uint16]bool{
		vkC:   false,
		vkEsc: false,
		vkY:   false,
		vkN:   false,
		vkR:   false,
		vkF:   false,
		vkS:   false,
	}
	for {
		r, _, _ := procReadConsoleInput.Call(handle, uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&read)))
		if r == 0 || read == 0 {
			// Fall back to async polling when console events are not coming through
			// ESC
			for vk, rChar := range map[uint16]rune{vkEsc: 27, vkC: 'C', vkY: 'Y', vkN: 'N', vkR: 'R', vkF: 'F', vkS: 'S'} {
				down := asyncDown(vk)
				if down && !prevAsync[vk] {
					select {
					case keyChan <- rChar:
					default:
					}
					prevAsync[vk] = true
				} else if !down {
					prevAsync[vk] = false
				}
			}
			// Small sleep to avoid busy looping
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if rec.EventType != keyEvent {
			continue
		}
		kev := (*keyEventRecord)(unsafe.Pointer(&rec.Event[0]))
		down := kev.KeyDown != 0
		vk := kev.VirtualKeyCode
		// ESC
		if _, ok := prevDown[vk]; ok {
			// Map virtual keys to runes for channel
			rChar := rune(0)
			switch vk {
			case vkEsc:
				rChar = 27
			case vkC:
				rChar = 'C'
			case vkY:
				rChar = 'Y'
			case vkN:
				rChar = 'N'
			case vkR:
				rChar = 'R'
			case vkF:
				rChar = 'F'
			case vkS:
				rChar = 'S'
			}
			if down && !prevDown[vk] {
				select {
				case keyChan <- rChar:
				default:
				}
			}
			prevDown[vk] = down
			continue
		}
	}
}

// asyncDown checks if the high-order bit is set for the given virtual key (key currently down)
func asyncDown(vk uint16) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	// If high-order bit is set, key is down
	return int16(r)>>15 != 0
}
