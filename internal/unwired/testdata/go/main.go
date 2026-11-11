// Package main is the unwired detector's Go fixture. See detector_test.go.
package main

import "example.com/unw/registry"

var commands []func()

func main() {
	registry.Run()
	for _, cmd := range commands {
		cmd()
	}
}
