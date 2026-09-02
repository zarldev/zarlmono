package transcript

// StrongerPersistence returns the policy that provides the stronger durability
// guarantee. Immediate dominates debounced, which dominates none.
func StrongerPersistence(left, right Persistence) Persistence {
	if persistenceRank(right) > persistenceRank(left) {
		return right
	}
	return left
}

func persistenceRank(policy Persistence) int {
	switch policy {
	case Persistences.PERSISTENCEIMMEDIATE:
		return 2
	case Persistences.PERSISTENCEDEBOUNCED:
		return 1
	default:
		return 0
	}
}
