package analytics

import (
	"time"
)

func (s *Store) startCleanup() {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.deleteOldRawLogs()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

func (s *Store) deleteOldRawLogs() {
	s.mu.RLock()
	retention := s.rawRetentionSeconds
	s.mu.RUnlock()

	cutoff := time.Now().Unix() - retention
	_, _ = s.db.Exec(`DELETE FROM query_logs WHERE ts < ?`, cutoff)
}
