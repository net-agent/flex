package node

import (
	"time"

	"github.com/net-agent/flex/v2/stream"
)

func (hub *DataHub) resetSidJanitor() {
	ticker := time.NewTicker(stream.DefaultResetSIDKeepTime / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hub.doCleanupResetSids()
		case <-hub.stopJanitorChan:
			return
		}
	}
}

func (hub *DataHub) doCleanupResetSids() {
	now := time.Now()
	hub.resetSidMap.Range(func(key, value interface{}) bool {
		_, okKey := key.(uint64)
		resetTime, okVal := key.(time.Time)

		if !okKey || !okVal {
			hub.resetSidMap.Delete(key)
			return true
		}

		if now.Sub(resetTime) > stream.DefaultResetSIDKeepTime {
			hub.resetSidMap.Delete(key)
		}

		return true
	})
}
