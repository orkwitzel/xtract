// Package reporter is the counter the extractor writes to and the UI reads
// from. That is deliberately all it is.
//
// It holds two numbers: how many archives are known about, and how many have
// been dealt with. The extractor bumps them as it discovers and finishes work,
// the UI reads them whenever it wants to draw. Neither side needs to know the
// other exists, and there is nothing here to get out of sync.
package reporter

import "sync/atomic"

// Stats is a consistent reading of both counters.
type Stats struct {
	Total     int // archives discovered so far, including nested ones
	Extracted int // archives finished, whether they succeeded or failed
}

// Remaining is how many archives are still outstanding.
func (s Stats) Remaining() int { return max(s.Total-s.Extracted, 0) }

// Ratio is progress from 0 to 1, and 0 before anything is known.
func (s Stats) Ratio() float64 {
	if s.Total == 0 {
		return 0
	}
	return min(float64(s.Extracted)/float64(s.Total), 1)
}

var (
	total     atomic.Int64
	extracted atomic.Int64
)

// AddTotal records n newly discovered archives.
func AddTotal(n int) { total.Add(int64(n)) }

// AddExtracted records n archives finished.
func AddExtracted(n int) { extracted.Add(int64(n)) }

// Get returns both counters.
//
// The two loads are not atomic with respect to each other, so a reading taken
// mid-update can show one archive discovered but not yet counted. For a
// progress display that is invisible, and it costs nothing to read.
func Get() Stats {
	return Stats{
		Total:     int(total.Load()),
		Extracted: int(extracted.Load()),
	}
}

// Reset clears both counters, for a fresh run or a test.
func Reset() {
	total.Store(0)
	extracted.Store(0)
}
