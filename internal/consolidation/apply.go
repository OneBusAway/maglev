package consolidation

// Outcome is what happens to one absorbed stop that a feed loaded.
type Outcome struct {
	Canonical StopID
	// Moved reports whether the absorbed stop's stop times move to
	// Canonical. When Canonical is not stored, because no feed has it or
	// the file absorbs it too, they are dropped instead.
	Moved bool
}

// Apply returns the outcome for each loaded stop the file absorbs. Every
// such stop is removed, even when its canonical stop is missing, because
// Java's bundle builder rejects absorbed stops before it looks up their
// replacements.
func (f *File) Apply(loaded map[StopID]Location) map[StopID]Outcome {
	replacements := f.Replacements()
	outcomes := make(map[StopID]Outcome)
	for absorbed, canonical := range replacements {
		if _, ok := loaded[absorbed]; !ok {
			continue
		}
		_, canonicalLoaded := loaded[canonical]
		_, canonicalAbsorbed := replacements[canonical]
		outcomes[absorbed] = Outcome{Canonical: canonical, Moved: canonicalLoaded && !canonicalAbsorbed}
	}
	return outcomes
}
