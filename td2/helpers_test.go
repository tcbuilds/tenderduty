package tenderduty

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	slashing "github.com/cosmos/cosmos-sdk/x/slashing/types"
	staking "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/gogo/protobuf/proto"
	abci "github.com/tendermint/tendermint/abci/types"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

// fakeABCIResult is the canned answer a fakeABCIQuerier returns for one query path.
type fakeABCIResult struct {
	response *ctypes.ResultABCIQuery
	err      error
}

// fakeABCIQuerier answers ABCI queries from a fixed table keyed by path and records the paths queried.
type fakeABCIQuerier struct {
	results map[string]fakeABCIResult
	calls   []string
}

func (f *fakeABCIQuerier) ABCIQuery(_ context.Context, path string, _ tmbytes.HexBytes) (*ctypes.ResultABCIQuery, error) {
	f.calls = append(f.calls, path)
	result, ok := f.results[path]
	if !ok {
		return nil, fmt.Errorf("fake querier: unexpected path %s", path)
	}
	return result.response, result.err
}

// errRateLimited mimics what the tendermint client returns when nginx answers with a 429 HTML page.
var errRateLimited = errors.New("error unmarshalling: invalid character '<' looking for beginning of value")

func okResult(value []byte) fakeABCIResult {
	return fakeABCIResult{response: &ctypes.ResultABCIQuery{Response: abci.ResponseQuery{Value: value}}}
}

func codeResult(code uint32, log string) fakeABCIResult {
	return fakeABCIResult{response: &ctypes.ResultABCIQuery{Response: abci.ResponseQuery{
		Code: code, Codespace: "sdk", Log: log,
	}}}
}

func errResult(err error) fakeABCIResult {
	return fakeABCIResult{err: err}
}

// testEd25519Key returns a deterministic ed25519 consensus key.
func testEd25519Key() *ed25519.PubKey {
	return ed25519.GenPrivKeyFromSecret([]byte("tenderduty-test")).PubKey().(*ed25519.PubKey)
}

func mustAny(t *testing.T, key proto.Message) *codectypes.Any {
	t.Helper()
	anyKey, err := codectypes.NewAnyWithValue(key)
	if err != nil {
		t.Fatalf("packing pubkey: %v", err)
	}
	return anyKey
}

func validatorValue(t *testing.T, moniker string, status staking.BondStatus, jailed bool, pubkey *codectypes.Any) []byte {
	t.Helper()
	response := staking.QueryValidatorResponse{Validator: staking.Validator{
		ConsensusPubkey: pubkey,
		Description:     staking.Description{Moniker: moniker},
		Status:          status,
		Jailed:          jailed,
	}}
	value, err := response.Marshal()
	if err != nil {
		t.Fatalf("marshalling validator: %v", err)
	}
	return value
}

func signingInfoValue(t *testing.T, tombstoned bool, missed int64) []byte {
	t.Helper()
	response := slashing.QuerySigningInfoResponse{ValSigningInfo: slashing.ValidatorSigningInfo{
		Tombstoned: tombstoned, MissedBlocksCounter: missed,
	}}
	value, err := response.Marshal()
	if err != nil {
		t.Fatalf("marshalling signing info: %v", err)
	}
	return value
}

func paramsValue(t *testing.T, window int64) []byte {
	t.Helper()
	response := slashing.QueryParamsResponse{Params: slashing.Params{SignedBlocksWindow: window}}
	value, err := response.Marshal()
	if err != nil {
		t.Fatalf("marshalling params: %v", err)
	}
	return value
}

func mustBech32(t *testing.T, prefix string, data []byte) string {
	t.Helper()
	encoded, err := bech32.ConvertAndEncode(prefix, data)
	if err != nil {
		t.Fatalf("encoding bech32: %v", err)
	}
	return encoded
}

// assertError checks err against an expected wrapped error and/or message fragment. A nil wantErr with an empty
// substring means no error is expected.
func assertError(t *testing.T, err, wantErr error, substr string) {
	t.Helper()
	if wantErr == nil && substr == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error (%v %q), got nil", wantErr, substr)
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Errorf("error %q does not wrap %q", err, wantErr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not contain %q", err, substr)
	}
}

// knownGoodValInfo returns validator info as it would look after a successful refresh.
func knownGoodValInfo(conspub []byte, valcons string) ValInfo {
	return ValInfo{Moniker: "good", Bonded: true, Missed: 3, Window: 10000, Conspub: conspub, Valcons: valcons}
}

func assertValInfo(t *testing.T, got, want ValInfo) {
	t.Helper()
	if got.Moniker != want.Moniker || got.Bonded != want.Bonded || got.Jailed != want.Jailed ||
		got.Tombstoned != want.Tombstoned || got.Missed != want.Missed || got.Window != want.Window ||
		got.Valcons != want.Valcons || string(got.Conspub) != string(want.Conspub) {
		t.Errorf("valInfo = %+v, want %+v", got, want)
	}
}
