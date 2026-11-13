package tenderduty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	staking "github.com/cosmos/cosmos-sdk/x/staking/types"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/p2p"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	rpctypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// testChainID is the chain id served by the fake tendermint nodes.
const testChainID = "test-1"

// nginxRateLimitPage is the body nginx serves with a 429 when limit_req rejects a request.
const nginxRateLimitPage = "<html>\r\n<head><title>429 Too Many Requests</title></head>\r\n" +
	"<body>\r\n<center><h1>429 Too Many Requests</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

// fakeTendermintNode answers the JSON-RPC "status" and "abci_query" methods from canned ABCI values.
type fakeTendermintNode struct {
	abciValues map[string][]byte
}

func (node fakeTendermintNode) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	var rpcRequest rpctypes.RPCRequest
	if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	var result interface{}
	switch rpcRequest.Method {
	case "status":
		result = &ctypes.ResultStatus{NodeInfo: p2p.DefaultNodeInfo{Network: testChainID}}
	case "abci_query":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(rpcRequest.Params, &params); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		result = &ctypes.ResultABCIQuery{Response: abci.ResponseQuery{Value: node.abciValues[params.Path]}}
	default:
		http.Error(writer, "unsupported method "+rpcRequest.Method, http.StatusBadRequest)
		return
	}
	if err := json.NewEncoder(writer).Encode(rpctypes.NewRPCSuccessResponse(rpcRequest.ID, result)); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}

// rateLimitedNode always answers like an nginx proxy enforcing limit_req.
func rateLimitedNode(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html")
	writer.WriteHeader(http.StatusTooManyRequests)
	_, _ = writer.Write([]byte(nginxRateLimitPage)) // write errors only mean the test client went away
}

func newTestClient(t *testing.T, remote string) *rpchttp.HTTP {
	t.Helper()
	client, err := rpchttp.New(remote, "/websocket")
	if err != nil {
		t.Fatalf("creating rpc client for %s: %v", remote, err)
	}
	return client
}

func TestIsUnusableResponseError(t *testing.T) {
	var target map[string]interface{}
	syntaxError := json.Unmarshal([]byte(nginxRateLimitPage), &target)
	tendermintError := fmt.Errorf("error unmarshalling: %w", syntaxError)
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"wrapped json syntax error", fmt.Errorf("querying validator: %w", tendermintError), true},
		{"unmarshalling message without wrapping", errors.New(errRateLimited.Error()), true},
		{"too many requests", errors.New("429 Too Many Requests"), true},
		{"deadline exceeded", fmt.Errorf("post failed: %w", context.DeadlineExceeded), false},
		{"abci code error", fmt.Errorf("query: %w", errABCIQueryFailed), false},
		{"connection refused", errors.New("post failed: dial tcp: connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnusableResponseError(tt.err); got != tt.want {
				t.Errorf("isUnusableResponseError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestMarkNodeDown(t *testing.T) {
	since := time.Unix(1000, 0)
	tests := []struct {
		name      string
		node      NodeConfig
		url       string
		wantMatch bool
		wantDown  bool
		wantSince bool
	}{
		{"healthy node goes down now", NodeConfig{Url: "http://a"}, "http://a", true, true, false},
		{"down node keeps original down time", NodeConfig{Url: "http://a", down: true, downSince: since}, "http://a", true, true, true},
		{"other url is untouched", NodeConfig{Url: "http://a"}, "http://b", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := tt.node
			cc := &ChainConfig{Nodes: []*NodeConfig{&node}}
			if got := cc.markNodeDown(tt.url, "rate limited"); got != tt.wantMatch {
				t.Errorf("markNodeDown() = %v, want %v", got, tt.wantMatch)
			}
			if node.down != tt.wantDown {
				t.Errorf("down = %v, want %v", node.down, tt.wantDown)
			}
			if tt.wantMatch && node.lastMsg != "rate limited" {
				t.Errorf("lastMsg = %q, want %q", node.lastMsg, "rate limited")
			}
			if tt.wantSince && !node.downSince.Equal(since) {
				t.Errorf("downSince = %v, want %v", node.downSince, since)
			}
		})
	}
}

// rotationFixture holds a chain config pointing at a rate limited node with known good validator info.
type rotationFixture struct {
	cc          *ChainConfig
	limited     *NodeConfig
	healthyURL  string
	priorInfo   ValInfo
	healthyInfo ValInfo
}

func newRotationFixture(t *testing.T, withHealthyNode bool) rotationFixture {
	t.Helper()
	key := testEd25519Key()
	valoper := mustBech32(t, "cosmosvaloper", make([]byte, consensusAddressLength))
	valcons := mustBech32(t, "cosmosvalcons", key.Address())

	limitedServer := httptest.NewServer(http.HandlerFunc(rateLimitedNode))
	t.Cleanup(limitedServer.Close)
	fixture := rotationFixture{limited: &NodeConfig{Url: limitedServer.URL}, priorInfo: knownGoodValInfo(key.Address(), valcons)}
	nodes := []*NodeConfig{fixture.limited}
	if withHealthyNode {
		healthyServer := httptest.NewServer(fakeTendermintNode{abciValues: map[string][]byte{
			stakingValidatorPath:    validatorValue(t, "good", staking.Bonded, false, mustAny(t, key)),
			slashingSigningInfoPath: signingInfoValue(t, false, 11),
		}})
		t.Cleanup(healthyServer.Close)
		fixture.healthyURL = healthyServer.URL
		nodes = append(nodes, &NodeConfig{Url: healthyServer.URL})
		fixture.healthyInfo = fixture.priorInfo
		fixture.healthyInfo.Missed = 11
	}
	prior := fixture.priorInfo
	fixture.cc = &ChainConfig{
		name: "test", ChainId: testChainID, ValAddress: valoper, Nodes: nodes,
		client: newTestClient(t, limitedServer.URL), valInfo: &prior, failedWsUrls: map[string]bool{},
	}
	return fixture
}

func TestGetValInfoRateLimitedKeepsLastKnownGood(t *testing.T) {
	fixture := newRotationFixture(t, false)
	err := fixture.cc.GetValInfo(false)
	if !isUnusableResponseError(err) {
		t.Fatalf("GetValInfo() error = %v, want an unusable response error", err)
	}
	assertValInfo(t, *fixture.cc.valInfo, fixture.priorInfo)
}

func TestRefreshValInfoRotatesAwayFromRateLimitedNode(t *testing.T) {
	fixture := newRotationFixture(t, true)
	fixture.cc.refreshValInfo()

	if !fixture.limited.down || fixture.limited.lastMsg == "" {
		t.Errorf("rate limited node down = %v (%q), want marked down", fixture.limited.down, fixture.limited.lastMsg)
	}
	if remote := fixture.cc.client.Remote(); remote != fixture.healthyURL {
		t.Errorf("active client = %s, want %s", remote, fixture.healthyURL)
	}
	assertValInfo(t, *fixture.cc.valInfo, fixture.healthyInfo)
	assertValInfo(t, *fixture.cc.lastValInfo, fixture.priorInfo)
}

func TestGetValInfoWithRotationNoHealthyNode(t *testing.T) {
	fixture := newRotationFixture(t, false)
	err := fixture.cc.getValInfoWithRotation(false)
	if err == nil {
		t.Fatal("getValInfoWithRotation() error = nil, want rotation failure")
	}
	if !fixture.limited.down {
		t.Error("rate limited node was not marked down")
	}
	assertValInfo(t, *fixture.cc.valInfo, fixture.priorInfo)
}

func TestGetValInfoWithRotationPassesThroughOtherErrors(t *testing.T) {
	fixture := newRotationFixture(t, false)
	fixture.cc.client = nil
	if err := fixture.cc.getValInfoWithRotation(false); err == nil || isUnusableResponseError(err) {
		t.Fatalf("getValInfoWithRotation() error = %v, want nil client error", err)
	}
	if fixture.limited.down {
		t.Error("node marked down for an error that is not a bad response")
	}
}

func TestRotateClient(t *testing.T) {
	tests := []struct {
		name            string
		swapBeforehand  bool
		nilUsedClient   bool
		wantErr         bool
		wantLimitedDown bool
		wantHealthy     bool
	}{
		{"active client rotates to healthy node", false, false, false, true, true},
		{"client already swapped is retried, not marked down", true, false, false, false, true},
		{"no used client", false, true, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRotationFixture(t, true)
			usedClient := fixture.cc.client
			if tt.nilUsedClient {
				usedClient = nil
			}
			if tt.swapBeforehand {
				fixture.cc.client = newTestClient(t, fixture.healthyURL)
			}
			swappedClient := fixture.cc.client
			err := fixture.cc.rotateClient(usedClient, errRateLimited)
			if (err != nil) != tt.wantErr {
				t.Fatalf("rotateClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if fixture.limited.down != tt.wantLimitedDown {
				t.Errorf("used node down = %v, want %v", fixture.limited.down, tt.wantLimitedDown)
			}
			if got := fixture.cc.client.Remote() == fixture.healthyURL; got != tt.wantHealthy {
				t.Errorf("active client = %s, want healthy node %v", fixture.cc.client.Remote(), tt.wantHealthy)
			}
			if tt.swapBeforehand && (fixture.cc.client != swappedClient || fixture.cc.Nodes[1].down) {
				t.Error("rotateClient replaced or marked down a client it did not use")
			}
		})
	}
}

// TestGetValInfoWithRotationClientSwappedDuringQuery has the run loop swap cc.client while the query to the rate
// limited node is in flight. The stale node must not be marked down and the retry must use the new client.
func TestGetValInfoWithRotationClientSwappedDuringQuery(t *testing.T) {
	fixture := newRotationFixture(t, true)
	cc := fixture.cc
	healthyClient := newTestClient(t, fixture.healthyURL)
	staleServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cc.mtx.Lock()
		cc.client = healthyClient
		cc.mtx.Unlock()
		rateLimitedNode(writer, request)
	}))
	t.Cleanup(staleServer.Close)
	stale := &NodeConfig{Url: staleServer.URL}
	cc.Nodes = append(cc.Nodes, stale)
	cc.client = newTestClient(t, staleServer.URL)

	if err := cc.getValInfoWithRotation(false); err != nil {
		t.Fatalf("getValInfoWithRotation() error = %v", err)
	}
	if stale.down || cc.Nodes[1].down {
		t.Errorf("nodes marked down: stale %v, healthy %v; want neither", stale.down, cc.Nodes[1].down)
	}
	if cc.activeClient() != healthyClient {
		t.Errorf("active client = %s, want the swapped-in %s", cc.activeClient().Remote(), fixture.healthyURL)
	}
	assertValInfo(t, *cc.valInfo, fixture.healthyInfo)
}

func TestSleepContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		duration time.Duration
		want     bool
	}{
		{"elapsed", context.Background(), time.Millisecond, true},
		{"cancelled", cancelled, time.Hour, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sleepContext(tt.ctx, tt.duration); got != tt.want {
				t.Errorf("sleepContext() = %v, want %v", got, tt.want)
			}
		})
	}
}
