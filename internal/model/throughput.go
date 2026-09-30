package model

import "time"

// How much a log is carrying (FR-13.17).
//
// A cluster keeps no rate. Kafka's brokers know one — they publish it over JMX —
// but nothing in the protocol a client speaks will say it, so the only way to
// answer "how busy is this topic" is to read what the cluster does keep, twice,
// and divide by the time between.
//
// What it keeps is totals: how many records have been written to each partition,
// and how much room the logs take. Both are counts of the whole life of the
// topic, which is why a rate needs two of them and why a window that has just
// opened has no rate to show yet.

// TopicTotals is how much a topic has carried, as the cluster can say.
type TopicTotals struct {
	// At is when the reading was taken, by this machine's clock: the rate is
	// the difference between two readings over the time between them, and the
	// cluster stamps neither.
	At time.Time

	// Records is the sum of the partitions' high watermarks — every record ever
	// written to the topic, whether or not it is still there. It never falls,
	// because deleting old records does not unwrite them.
	Records int64

	// Bytes is how much room the topic's logs take on the brokers that lead
	// them. Unlike Records it can fall, because retention deletes whole
	// segments, and a reading lower than the one before it is that rather than
	// a negative rate.
	Bytes int64

	// Sized is false where the brokers would not say how much room the logs
	// take. The records can still be counted; the bytes cannot, and a nought
	// would read as an empty topic rather than as an unanswered question.
	Sized bool
}

// Rate is how much a topic carried over one interval: what two readings and the
// time between them say, per second.
type Rate struct {
	// At is the end of the interval the rate is for.
	At time.Time

	// Records and Bytes are per second. Bytes is -1 where it is not known,
	// which is either a cluster that will not say how big its logs are or an
	// interval in which retention deleted more than was written.
	Records float64
	Bytes   float64
}

// Known reports whether the rate has a byte count in it.
func (r Rate) Known() bool { return r.Bytes >= 0 }

// RateBetween is the rate two readings make. It is false where they cannot make
// one: the same reading twice, or a clock that went backwards.
func RateBetween(before, after TopicTotals) (Rate, bool) {
	seconds := after.At.Sub(before.At).Seconds()
	if seconds <= 0 {
		return Rate{}, false
	}
	r := Rate{At: after.At, Bytes: -1}
	if n := after.Records - before.Records; n > 0 {
		r.Records = float64(n) / seconds
	}
	if before.Sized && after.Sized {
		if n := after.Bytes - before.Bytes; n >= 0 {
			r.Bytes = float64(n) / seconds
		}
		// And where it fell, the bytes are not known for this interval: a
		// segment went, which says nothing about what was written into it.
	}
	return r, true
}
