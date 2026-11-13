package tenderduty

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// concurrentIterations is how many times each goroutine touches the failed websocket set in the race test.
const concurrentIterations = 50

func TestFailedWsURLTracking(t *testing.T) {
	tests := []struct {
		name       string
		record     []string
		reset      bool
		query      string
		wantFailed bool
		wantAny    bool
	}{
		{"nothing recorded", nil, false, "http://a", false, false},
		{"recorded url", []string{"http://a"}, false, "http://a", true, true},
		{"other url", []string{"http://a"}, false, "http://b", false, true},
		{"reset clears", []string{"http://a", "http://b"}, true, "http://a", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &ChainConfig{}
			for _, nodeURL := range tt.record {
				cc.recordFailedWsURL(nodeURL)
			}
			if tt.reset {
				cc.resetFailedWsURLs()
			}
			if got := cc.isWsURLFailed(tt.query); got != tt.wantFailed {
				t.Errorf("isWsURLFailed(%s) = %v, want %v", tt.query, got, tt.wantFailed)
			}
			if got := cc.hasFailedWsURLs(); got != tt.wantAny {
				t.Errorf("hasFailedWsURLs() = %v, want %v", got, tt.wantAny)
			}
		})
	}
}

// TestFailedWsURLsConcurrentAccess runs node selection (as node rotation does from the health monitor) while the
// run loop records and resets websocket failures. Run with -race: unguarded map access is a fatal runtime error.
func TestFailedWsURLsConcurrentAccess(t *testing.T) {
	first := httptest.NewServer(fakeTendermintNode{})
	t.Cleanup(first.Close)
	second := httptest.NewServer(fakeTendermintNode{})
	t.Cleanup(second.Close)
	cc := &ChainConfig{
		name: "test", ChainId: testChainID,
		Nodes: []*NodeConfig{{Url: first.URL}, {Url: second.URL}},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < concurrentIterations; i++ {
			if err := cc.newRpc(); err != nil {
				t.Errorf("newRpc() error = %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < concurrentIterations; i++ {
			cc.recordFailedWsURL(first.URL)
			if i%2 == 0 {
				cc.resetFailedWsURLs()
			}
		}
	}()
	wg.Wait()

	if cc.activeClient() == nil {
		t.Fatal("no client selected after concurrent node selection")
	}
}

func TestRecordWsExit(t *testing.T) {
	const dialed, current = "http://dialed", "http://current"
	tests := []struct {
		name          string
		dialedURL     string
		lastBlock     int64
		wantReceived  bool
		wantDialed    bool
		wantAnyFailed bool
	}{
		{"no blocks records dialed node", dialed, 5, false, true, true},
		{"blocks received resets failures", dialed, 6, true, false, false},
		{"nothing dialed records nothing", "", 5, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &ChainConfig{lastBlockNum: tt.lastBlock, client: newTestClient(t, current)}
			cc.recordFailedWsURL("http://older")
			cc.resetFailedWsURLs()
			if got := cc.recordWsExit(tt.dialedURL, 5); got != tt.wantReceived {
				t.Errorf("recordWsExit() = %v, want %v", got, tt.wantReceived)
			}
			if got := cc.isWsURLFailed(dialed); got != tt.wantDialed {
				t.Errorf("dialed node failed = %v, want %v", got, tt.wantDialed)
			}
			if cc.isWsURLFailed(current) {
				t.Error("current client node recorded as failed, want only the dialed node")
			}
			if got := cc.hasFailedWsURLs(); got != tt.wantAnyFailed {
				t.Errorf("hasFailedWsURLs() = %v, want %v", got, tt.wantAnyFailed)
			}
		})
	}
}

// TestWsRunReportsDialedURL rotates cc.client to another node while the websocket handshake is in flight and checks
// that the node that refused the websocket, not the rotated-to node, is reported and recorded as failed.
func TestWsRunReportsDialedURL(t *testing.T) {
	rotatedTo := httptest.NewServer(fakeTendermintNode{})
	t.Cleanup(rotatedTo.Close)
	cc := &ChainConfig{name: "test", ChainId: testChainID, valInfo: &ValInfo{Conspub: testEd25519Key().Address()}}
	rotatedClient := newTestClient(t, rotatedTo.URL)
	dialed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cc.mtx.Lock()
		cc.client = rotatedClient
		cc.mtx.Unlock()
		http.NotFound(writer, request)
	}))
	t.Cleanup(dialed.Close)
	cc.client = newTestClient(t, dialed.URL)

	got := cc.WsRun()
	if got != dialed.URL {
		t.Fatalf("WsRun() = %q, want dialed node %q", got, dialed.URL)
	}
	if cc.activeClient() != rotatedClient {
		t.Fatal("test setup: client was not rotated during the websocket handshake")
	}
	cc.recordWsExit(got, cc.lastBlockNum)
	if !cc.isWsURLFailed(dialed.URL) || cc.isWsURLFailed(rotatedTo.URL) {
		t.Errorf("failed websockets = %v, want only %s", cc.failedWsUrls, dialed.URL)
	}
}
