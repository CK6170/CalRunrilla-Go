package main

import (
	"fmt"
)

func debugPrintf(enabled bool, format string, a ...interface{}) {
	if enabled {
		fmt.Print("\033[33m")
		fmt.Printf("[DEBUG] "+format, a...)
		fmt.Print("\033[0m")
	}
}

func greenPrintf(format string, a ...interface{}) {
	fmt.Print("\033[92m")
	fmt.Printf(format, a...)
	fmt.Print("\033[0m")
}

func warningPrintf(format string, a ...interface{}) {
	fmt.Print("\033[93m")
	fmt.Printf(format, a...)
	fmt.Print("\033[0m")
}

func clearScreen() {
	fmt.Print("\033[2J\033[1;1H")
}

// ...add other UI helpers and prompts as needed...
