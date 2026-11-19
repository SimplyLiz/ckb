package check

// Check is implemented by alpha and Beta; only alpha is ever built.
type Check interface {
	Run() string
}

// All returns the checks the registry runs.
func All() []Check {
	return []Check{&alpha{}}
}
