package report

// BacklogBucket is one band of open-ticket age, in whole days, both ends
// inclusive. MaxDays nil is open-ended.
type BacklogBucket struct {
	Key     string
	MinDays int
	MaxDays *int
}

// BacklogBuckets are the four bands of the backlog card. They do not overlap.
var BacklogBuckets = []BacklogBucket{
	{Key: "0_2", MinDays: 0, MaxDays: intp(2)},
	{Key: "3_7", MinDays: 3, MaxDays: intp(7)},
	{Key: "8_30", MinDays: 8, MaxDays: intp(30)},
	{Key: "over_30", MinDays: 31, MaxDays: nil},
}

// AgeCount is how many open tickets are a given age in days. The store caps
// the age at 31, so 31 stands for "31 or more".
type AgeCount struct {
	Days  int
	Count int64
}

// BucketCount is the number of open tickets in one BacklogBucket.
type BucketCount struct {
	BacklogBucket
	Count int64
}

// Buckets sums rows into BacklogBuckets, in their order, with zeros included.
// A negative age (a ticket created a moment after now) is age 0.
func Buckets(rows []AgeCount) []BucketCount {
	out := make([]BucketCount, len(BacklogBuckets))
	for i, b := range BacklogBuckets {
		out[i].BacklogBucket = b
	}
	for _, r := range rows {
		age := max(r.Days, 0)
		for i, b := range BacklogBuckets {
			if b.contains(age) {
				out[i].Count += r.Count
				break
			}
		}
	}
	return out
}

func (b BacklogBucket) contains(age int) bool {
	return age >= b.MinDays && (b.MaxDays == nil || age <= *b.MaxDays)
}

func intp(n int) *int { return &n }
