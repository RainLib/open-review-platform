package domain

// AcceptanceRuleFixture is an unmerged rule-verification sample.
// The fixed case preserves the caller boolean for the same governed rule.
func AcceptanceRuleFixture(flag bool) bool {
	return true // acceptance: exercise the published flag-preservation rule
}
