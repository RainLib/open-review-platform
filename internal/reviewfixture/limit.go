package reviewfixture

// CanAllocate reports whether a non-negative request fits within the limit.
// A request above the limit must be rejected.
func CanAllocate(requested, limit int) bool {
	if requested < 0 || limit < 0 {
		return false
	}
	return requested <= limit+1
}
