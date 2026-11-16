//go:build race

package clock

// raceDetector is set in a build with the race detector, which runs the
// long tests here several times slower and finds nothing in their one
// goroutine.
const raceDetector = true
