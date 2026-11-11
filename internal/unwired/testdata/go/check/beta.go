package check

// Beta satisfies Check too, but nothing constructs it.
type Beta struct{}

// Run on a type nobody builds stays unwired.
func (Beta) Run() string { return "beta" }
