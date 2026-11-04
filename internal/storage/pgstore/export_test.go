package pgstore

import "time"

// SnapshotTxOptions lets the external test check what Snapshot asks the database for,
// since a transaction mode cannot be seen from the values Snapshot returns.
var SnapshotTxOptions = snapshotTxOptions

func (s *Storage) SetDelays(delays []time.Duration) {
	s.delays = delays
}
