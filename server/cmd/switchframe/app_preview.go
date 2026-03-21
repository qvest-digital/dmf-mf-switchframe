package main

// parsePreviewResolution converts a resolution string to width and height.
func parsePreviewResolution(res string) (int, int) {
	switch res {
	case "360p":
		return 640, 360
	case "480p":
		return 852, 480 // even-aligned (854 is odd, breaks NV12 UV plane math on GPU)
	case "720p":
		return 1280, 720
	default:
		return 852, 480
	}
}

// parseRelayResolution converts a resolution string to width and height.
// Returns (0, 0) for "source" meaning no scaling (use source resolution).
func parseRelayResolution(res string) (int, int) {
	switch res {
	case "source":
		return 0, 0
	case "360p":
		return 640, 360
	case "480p":
		return 852, 480
	case "720p":
		return 1280, 720
	default:
		return 1280, 720
	}
}
