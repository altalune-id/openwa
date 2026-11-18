package templates

// SessionStatus is how a number's session state reads: its label key, its tone and its icon.
type SessionStatus struct {
	Key  string
	Tone Tone
	Icon string
}
