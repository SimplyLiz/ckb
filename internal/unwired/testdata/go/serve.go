package main

import "example.com/unw/server"

// Registered the cobra way: nothing references this file, init runs anyway.
func init() {
	commands = append(commands, func() { server.Start() })
}
