package tenderduty

import (
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
