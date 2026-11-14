//go:build race

package main

// raceDetector is set in a build with the race detector, which runs the
// long single-goroutine tests here several times slower.
const raceDetector = true
