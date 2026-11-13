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
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
)

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
		{"abci code error with marker log", abciLogError(t, codeResult(1, "error unmarshalling: 429 too many requests")), false},
		{"empty abci response with marker", fmt.Errorf("too many requests: %w", errEmptyABCIResponse), false},
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

// abciLogError returns the error queryABCI produces for a canned ABCI answer.
func abciLogError(t *testing.T, result fakeABCIResult) error {
	t.Helper()
	querier := &fakeABCIQuerier{results: map[string]fakeABCIResult{stakingValidatorPath: result}}
	_, err := queryABCI(context.Background(), querier, stakingValidatorPath, nil)
	if err == nil {
		t.Fatal("queryABCI() error = nil, want an ABCI error")
	}
	return err
}

// TestGetValInfoWithRotationABCIErrorKeepsNode has a node answer with an application error whose log contains
// the rate limit markers. The node answered with valid JSON-RPC, so it must not be marked down or rotated away.
func TestGetValInfoWithRotationABCIErrorKeepsNode(t *testing.T) {
	failing := httptest.NewServer(fakeTendermintNode{abciFailures: map[string]abci.ResponseQuery{
		stakingValidatorPath: {Code: 5, Codespace: "staking", Log: "error unmarshalling request: too many requests"},
	}})
	t.Cleanup(failing.Close)
	fixture := newRotationFixture(t, true)
	cc := fixture.cc
	failingNode := &NodeConfig{Url: failing.URL}
	cc.Nodes = append([]*NodeConfig{failingNode}, cc.Nodes...)
	failingClient := newTestClient(t, failing.URL)
	cc.client = failingClient

	err := cc.getValInfoWithRotation(false)
	assertError(t, err, errABCIQueryFailed, "too many requests")
	if failingNode.down {
		t.Error("node answering with an ABCI application error was marked down")
	}
	if cc.activeClient() != failingClient {
		t.Errorf("active client = %s, want it kept on %s", cc.activeClient().Remote(), failing.URL)
	}
	assertValInfo(t, *cc.valInfo, fixture.priorInfo)
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
	limitedClient := fixture.cc.client
	err := fixture.cc.getValInfoWithRotation(false)
	assertError(t, err, errNoHealthyAlternative, "")
	if fixture.limited.down {
		t.Error("only node was marked down although there is nothing to rotate to")
	}
	if fixture.cc.activeClient() != limitedClient || fixture.cc.noNodes {
		t.Errorf("client replaced or chain flagged as having no nodes (noNodes %v)", fixture.cc.noNodes)
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

// rotationChainName is the chain name used by rotation tests that inspect the global alarm cache.
const rotationChainName = "rotation-test"

// rotateClientCase describes one rotateClient scenario starting from a rate limited active node.
type rotateClientCase struct {
	name            string
	withHealthyNode bool
	alternativeDown bool
	swapBeforehand  bool
	nilUsedClient   bool
	wantErr         bool
	wantErrIs       error
	wantLimitedDown bool
	wantHealthy     bool
}

func TestRotateClient(t *testing.T) {
	tests := []rotateClientCase{
		{name: "active client rotates to healthy node", withHealthyNode: true, wantLimitedDown: true, wantHealthy: true},
		{name: "client already swapped is retried, not marked down", withHealthyNode: true, swapBeforehand: true, wantHealthy: true},
		{name: "no used client", withHealthyNode: true, nilUsedClient: true, wantErr: true},
		{name: "single node keeps current client", wantErr: true, wantErrIs: errNoHealthyAlternative},
		{name: "only alternative is down", withHealthyNode: true, alternativeDown: true, wantErr: true,
			wantErrIs: errNoHealthyAlternative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRotationFixture(t, tt.withHealthyNode)
			cc := fixture.cc
			cc.name = rotationChainName
			seedAlarm(t, rotationChainName)
			usedClient := cc.client
			if tt.nilUsedClient {
				usedClient = nil
			}
			if tt.alternativeDown {
				cc.Nodes[1].down = true
			}
			if tt.swapBeforehand {
				cc.client = newTestClient(t, fixture.healthyURL)
			}
			outcome := rotateOutcome{clientBefore: cc.client}
			outcome.err = cc.rotateClient(usedClient, errRateLimited)
			assertRotateClient(t, tt, fixture, outcome)
		})
	}
}

// rotateOutcome is the active client before a rotateClient call and the error the call returned.
type rotateOutcome struct {
	clientBefore *rpchttp.HTTP
	err          error
}

func assertRotateClient(t *testing.T, tt rotateClientCase, fixture rotationFixture, outcome rotateOutcome) {
	t.Helper()
	cc, err := fixture.cc, outcome.err
	if (err != nil) != tt.wantErr || (tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs)) {
		t.Fatalf("rotateClient() error = %v, wantErr %v (%v)", err, tt.wantErr, tt.wantErrIs)
	}
	if fixture.limited.down != tt.wantLimitedDown {
		t.Errorf("used node down = %v, want %v", fixture.limited.down, tt.wantLimitedDown)
	}
	if got := cc.client.Remote() == fixture.healthyURL; got != tt.wantHealthy {
		t.Errorf("active client = %s, want healthy node %v", cc.client.Remote(), tt.wantHealthy)
	}
	if !tt.wantLimitedDown && cc.client != outcome.clientBefore {
		t.Error("rotateClient replaced the client without rotating away from the used node")
	}
	if cc.noNodes || cc.lastError != "" || alarms.getCount(rotationChainName) == 0 {
		t.Errorf("no-nodes handling ran: noNodes %v, lastError %q, alarms %d",
			cc.noNodes, cc.lastError, alarms.getCount(rotationChainName))
	}
}

// seedAlarm records an active alarm for chain so tests can detect alarms.clearAll.
func seedAlarm(t *testing.T, chain string) {
	t.Helper()
	alarms.notifyMux.Lock()
	alarms.AllAlarms[chain] = map[string]time.Time{"seeded alarm": time.Now()}
	alarms.notifyMux.Unlock()
	t.Cleanup(func() {
		alarms.notifyMux.Lock()
		delete(alarms.AllAlarms, chain)
		alarms.notifyMux.Unlock()
	})
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
