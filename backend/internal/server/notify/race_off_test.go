//go:build !race

package notify

// raceEnabled lets a timing test allow for the race detector's cost.
const raceEnabled = false
