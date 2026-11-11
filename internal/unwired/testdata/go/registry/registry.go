package registry

import "example.com/unw/check"

// Run calls every check through the interface.
func Run() {
	for _, c := range check.All() {
		_ = c.Run()
	}
}
