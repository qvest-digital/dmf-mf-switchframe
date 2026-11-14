//go:build race

package playout

// raceDetector is set in a build with the race detector, which runs the
// long tests here several times slower.
const raceDetector = true
