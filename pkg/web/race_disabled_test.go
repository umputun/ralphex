//go:build !race

package web

// raceEnabled reports whether the race detector is active in this test build.
// used to skip tests whose runtime under -race exceeds the test binary timeout.
const raceEnabled = false
