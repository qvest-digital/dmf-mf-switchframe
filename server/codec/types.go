package codec

// EncoderInfo describes an available video encoder backend.
type EncoderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
}
