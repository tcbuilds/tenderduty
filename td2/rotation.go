package tenderduty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
)

// Markers found in tendermint JSON-RPC client errors when a node's reply is not a JSON-RPC envelope, for example
// an nginx "429 Too Many Requests" HTML page served by a rate limiting proxy.
const (
	unmarshalFailureMarker = "error unmarshalling"
	tooManyRequestsMarker  = "too many requests"
)

// isUnusableResponseError reports whether err means the node answered with something other than a JSON-RPC
// response (rate limiting, proxy error pages). Retrying against the same node is unlikely to help. A well formed
// ABCI answer (a non-zero code or an empty value) proves the node works, so it never counts, even when the
// application's log text happens to contain one of the markers.
func isUnusableResponseError(err error) bool {
	if err == nil || errors.Is(err, errABCIQueryFailed) || errors.Is(err, errEmptyABCIResponse) {
		return false
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, unmarshalFailureMarker) || strings.Contains(message, tooManyRequestsMarker)
}

// activeClient returns the RPC client currently used for queries and the websocket, or nil if none is set.
func (cc *ChainConfig) activeClient() *rpchttp.HTTP {
	cc.mtx.RLock()
	defer cc.mtx.RUnlock()
	return cc.client
}

// errNoHealthyAlternative is returned when a node returns unusable responses but no other node can replace it, in
// which case the current client and the last known validator info are kept.
var errNoHealthyAlternative = errors.New("no other healthy RPC node to rotate to")

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

// rotateClient switches the active RPC client away from usedClient, the client whose query failed with cause, and
// marks its node down. If cc.client was already replaced (for example by the run loop) the failure belongs to a
// node that is no longer active, so nothing is marked down and the caller should simply retry on the current client.
func (cc *ChainConfig) rotateClient(usedClient *rpchttp.HTTP, cause error) error {
	if usedClient == nil {
		return errors.New("no active rpc client to rotate away from")
	}
	cc.rpcSetupMtx.Lock()
	defer cc.rpcSetupMtx.Unlock()
	remote := usedClient.Remote()
	if cc.activeClient() != usedClient {
		l(fmt.Sprintf("🔀 %-12s RPC client already moved away from %s, retrying on the current node", cc.name, remote))
		return nil
	}
	if !cc.hasHealthyAlternative(remote) {
		return fmt.Errorf("keeping RPC client on %s: %w", remote, errNoHealthyAlternative)
	}
	cc.markNodeDown(remote, fmt.Sprintf("%-12s node %s is rate limited or returned a non JSON-RPC response", cc.name, remote))
	l(fmt.Sprintf("🔀 %-12s rotating RPC client away from %s: %v", cc.name, remote, cause))
	// selectUsableNode, not newRpc: if every alternative fails verification the current client still answers,
	// so the chain must not be flagged as having no nodes nor have its alarms cleared.
	if !cc.selectUsableNode() {
		return fmt.Errorf("rotating RPC client away from %s: %w", remote, errNoHealthyAlternative)
	}
	return nil
}

// hasHealthyAlternative reports whether any configured node other than excludedURL is currently not marked down.
func (cc *ChainConfig) hasHealthyAlternative(excludedURL string) bool {
	for _, node := range cc.Nodes {
		if node.Url != excludedURL && !node.down {
			return true
		}
	}
	return false
}

// getValInfoWithRotation fetches validator info and, when the queried node returns an unusable response, rotates
// away from that node and retries once on the active client so a rate limited node is not queried in a loop.
func (cc *ChainConfig) getValInfoWithRotation(first bool) error {
	client := cc.activeClient()
	err := cc.getValInfoFrom(client, first)
	if !isUnusableResponseError(err) {
		return err
	}
	if rotateErr := cc.rotateClient(client, err); rotateErr != nil {
		return fmt.Errorf("validator info query failed (%v) and %w", err, rotateErr)
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
