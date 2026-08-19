package db

//go:generate go tool goenums -f api_key_storage_enum.go

type apiKeyStorage int

const (
	invalid   apiKeyStorage = iota // invalid invalid
	vault                          // vault
	plaintext                      // plaintext
)
