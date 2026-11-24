package report

import "time"

// Period is an inclusive run of calendar days, From through To.
type Period struct {
	From, To Day
}

// Days is the number of days in the period, both ends counted. A period whose
// To is before its From has no days this function is meant for; ParseQuery and
// Query.Period never produce one.
func (p Period) Days() int {
	return int(p.To.Time().Sub(p.From.Time())/(24*time.Hour)) + 1
}

// Previous is the period of the same length that ends the day before p begins.
func (p Period) Previous() Period {
	n := p.Days()
	return Period{From: p.From.Add(-n), To: p.From.Add(-1)}
}
