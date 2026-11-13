package tenderduty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Markers found in tendermint JSON-RPC client errors when a node's reply is not a JSON-RPC envelope, for example
// an nginx "429 Too Many Requests" HTML page served by a rate limiting proxy.
const (
	unmarshalFailureMarker = "error unmarshalling"
	tooManyRequestsMarker  = "too many requests"
)

// isUnusableResponseError reports whether err means the node answered with something other than a JSON-RPC
// response (rate limiting, proxy error pages). Retrying against the same node is unlikely to help.
func isUnusableResponseError(err error) bool {
	if err == nil {
		return false
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, unmarshalFailureMarker) || strings.Contains(message, tooManyRequestsMarker)
}

// markDown flags the node as down, recording when it first went down and why.
func (node *NodeConfig) markDown(msg string) {
	if !node.down {
		node.down = true
		node.downSince = time.Now()
	}
	node.lastMsg = msg
}

// markNodeDown flags the configured node with the given URL as down and reports whether a node matched.
func (cc *ChainConfig) markNodeDown(nodeURL, msg string) bool {
	for _, node := range cc.Nodes {
		if node.Url == nodeURL {
			node.markDown(msg)
			return true
		}
	}
	return false
}

// rotateClient marks the node behind the active RPC client down because of cause and switches to another usable
// node using newRpc, which skips nodes that are marked down while any healthy node remains.
func (cc *ChainConfig) rotateClient(cause error) error {
	cc.mtx.RLock()
	client := cc.client
	cc.mtx.RUnlock()
	if client == nil {
		return errors.New("no active rpc client to rotate away from")
	}
	remote := client.Remote()
	cc.markNodeDown(remote, fmt.Sprintf("%-12s node %s is rate limited or returned a non JSON-RPC response", cc.name, remote))
	l(fmt.Sprintf("🔀 %-12s rotating RPC client away from %s: %v", cc.name, remote, cause))
	if err := cc.newRpc(); err != nil {
		return fmt.Errorf("rotating RPC client away from %s: %w", remote, err)
	}
	return nil
}

// getValInfoWithRotation fetches validator info and, when the active node returns an unusable response, marks it
// down, rotates to another node and retries once so a rate limited node is not queried in a loop.
func (cc *ChainConfig) getValInfoWithRotation(first bool) error {
	err := cc.GetValInfo(first)
	if !isUnusableResponseError(err) {
		return err
	}
	if err = cc.rotateClient(err); err != nil {
		return err
	}
	return cc.GetValInfo(first)
}

// sleepContext waits for duration and reports false if ctx was cancelled first.
func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
