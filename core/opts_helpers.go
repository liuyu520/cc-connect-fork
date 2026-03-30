package core

// OptsString extracts a string value from an opts map.
// Returns empty string if key is missing or not a string.
func OptsString(opts map[string]any, key string) string {
	v, _ := opts[key].(string)
	return v
}

// OptsBool extracts a bool value from an opts map.
// Returns false if key is missing or not a bool.
func OptsBool(opts map[string]any, key string) bool {
	v, _ := opts[key].(bool)
	return v
}
