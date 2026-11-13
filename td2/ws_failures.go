package tenderduty

// The failedWsUrls set is written by the per-chain run loop when a websocket exits and read by newRpc, which can
// run on the run loop or on the health monitor goroutine (node rotation). Every access goes through these helpers,
// which hold wsFailMtx only for the map operation itself and never acquire another lock while holding it.

// isWsURLFailed reports whether the websocket at nodeURL failed since the last time blocks were received.
func (cc *ChainConfig) isWsURLFailed(nodeURL string) bool {
	cc.wsFailMtx.Lock()
	defer cc.wsFailMtx.Unlock()
	return cc.failedWsUrls[nodeURL]
}

// hasFailedWsURLs reports whether any websocket URL is currently recorded as failed.
func (cc *ChainConfig) hasFailedWsURLs() bool {
	cc.wsFailMtx.Lock()
	defer cc.wsFailMtx.Unlock()
	return len(cc.failedWsUrls) > 0
}

// recordFailedWsURL remembers that the websocket at nodeURL failed so newRpc prefers other nodes.
func (cc *ChainConfig) recordFailedWsURL(nodeURL string) {
	cc.wsFailMtx.Lock()
	defer cc.wsFailMtx.Unlock()
	if cc.failedWsUrls == nil {
		cc.failedWsUrls = make(map[string]bool)
	}
	cc.failedWsUrls[nodeURL] = true
}

// resetFailedWsURLs forgets all recorded websocket failures.
func (cc *ChainConfig) resetFailedWsURLs() {
	cc.wsFailMtx.Lock()
	defer cc.wsFailMtx.Unlock()
	cc.failedWsUrls = make(map[string]bool)
}

// recordWsExit handles a websocket that stopped. If blocks arrived since blockBefore the websocket was working, so
// recorded failures are forgotten and true is returned. Otherwise dialedURL, the node the websocket actually
// dialed (not whatever cc.client points at now), is recorded as failed so newRpc tries a different node.
func (cc *ChainConfig) recordWsExit(dialedURL string, blockBefore int64) bool {
	if cc.lastBlockNum > blockBefore {
		cc.resetFailedWsURLs()
		return true
	}
	if dialedURL != "" {
		cc.recordFailedWsURL(dialedURL)
	}
	return false
}
