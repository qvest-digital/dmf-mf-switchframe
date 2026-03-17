package main

// parsePreviewResolution converts a resolution string to width and height.
func parsePreviewResolution(res string) (int, int) {
	switch res {
	case "360p":
		return 640, 360
	case "480p":
		return 854, 480
	case "720p":
		return 1280, 720
	default:
		return 854, 480
	}
}
