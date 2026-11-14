package cartographer

// A library that serializes a struct in snake_case while this package's tags
// are camelCase (v4.0.3 and older) decodes without error into zero values.
// These checks tell a real result from such an empty one, so callers can say
// so instead of shipping zeros as findings.

// ErrUndecodable is returned in place of a result whose identifying fields
// decoded empty.
var ErrUndecodable = &CartographerError{"the linked library returned fields this CKB cannot read (snake_case JSON from a library older than this CKB expects); update the Cartographer library"}

// ShotgunDecoded reports whether shotgun-surgery entries carry their counts.
// An empty list is a valid answer.
func ShotgunDecoded(entries []ShotgunSurgeryEntry) bool {
	if len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		if e.PartnerCount > 0 || e.TotalCochanges > 0 {
			return true
		}
	}
	return false
}

// EvolutionDecoded reports whether an evolution result carries its trend and
// health scores. No snapshots is a valid answer.
func EvolutionDecoded(r *EvolutionResult) bool {
	if r == nil {
		return false
	}
	if len(r.Snapshots) == 0 {
		return true
	}
	if r.HealthTrend != "" {
		return true
	}
	for _, s := range r.Snapshots {
		if s.HealthScore != 0 {
			return true
		}
	}
	return false
}
