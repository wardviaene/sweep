package config

// Transcribe holds the transcription settings (docs/transcription.md). A nil
// Enabled follows the -transcribe flag (on when the model file exists).
type Transcribe struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (t *Transcribe) clone() *Transcribe {
	if t == nil {
		return nil
	}
	c := *t
	if t.Enabled != nil {
		v := *t.Enabled
		c.Enabled = &v
	}
	return &c
}
