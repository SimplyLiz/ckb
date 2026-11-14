package check

type alpha struct{}

// Run satisfies Check; it is only ever called through the interface.
func (alpha) Run() string { return "alpha" }

// Extra is not in the interface and nothing calls it.
func (alpha) Extra() string { return "extra" }
