package check

// All returns every built-in check in execution order.
func All() []Checker {
	return []Checker{
		SkewCheck{},
		DeprecatedAPICheck{},
		HelmCheck{},
		PDBCheck{},
		AvailabilityCheck{},
		DrainSimulation{},
	}
}
