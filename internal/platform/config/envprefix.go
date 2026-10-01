package config

// EnvPrefix is the environment-variable prefix every config key binds under.
const EnvPrefix = "OPENWA"

// EnvVar returns the environment variable name for a dotted config key.
func EnvVar(key string) string { return envVarName(EnvPrefix, key) }
