package tenderduty

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
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

func TestGetVal(t *testing.T) {
	key := testEd25519Key()
	secpKey := secp256k1.GenPrivKeyFromSecret([]byte("tenderduty-test")).PubKey().(*secp256k1.PubKey)
	valoper := mustBech32(t, "cosmosvaloper", make([]byte, consensusAddressLength))
	valcons := mustBech32(t, "cosmosvalcons", key.Address())

	tests := []struct {
		name      string
		address   string
		result    fakeABCIResult
		want      stakingValidator
		wantErr   error
		errSubstr string
	}{
		{"valcons address skips query", valcons, fakeABCIResult{},
			stakingValidator{conspub: key.Address(), moniker: valcons, bonded: true}, nil, ""},
		{"invalid valcons address", "cosmosvalcons1invalid", fakeABCIResult{},
			stakingValidator{}, nil, "could not decode consensus address"},
		{"rpc error is returned not masked", valoper, errResult(errRateLimited),
			stakingValidator{}, errRateLimited, "invalid character '<'"},
		{"non-zero code", valoper, codeResult(5, "validator does not exist"),
			stakingValidator{}, errABCIQueryFailed, "validator does not exist"},
		{"empty value", valoper, okResult(nil), stakingValidator{}, errEmptyABCIResponse, ""},
		{"garbage value", valoper, okResult([]byte{0xff, 0xff}), stakingValidator{}, nil, "decoding validator"},
		{"missing pubkey", valoper, okResult(validatorValue(t, "val", staking.Bonded, false, nil)),
			stakingValidator{}, nil, "missing consensus pubkey"},
		{"bonded ed25519", valoper, okResult(validatorValue(t, "val", staking.Bonded, false, mustAny(t, key))),
			stakingValidator{conspub: key.Address(), moniker: "val", bonded: true}, nil, ""},
		{"jailed secp256k1", valoper, okResult(validatorValue(t, "jv", staking.Unbonding, true, mustAny(t, secpKey))),
			stakingValidator{conspub: secpKey.Address(), moniker: "jv", jailed: true}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeABCIQuerier{results: map[string]fakeABCIResult{stakingValidatorPath: tt.result}}
			got, err := getVal(context.Background(), client, tt.address)
			assertError(t, err, tt.wantErr, tt.errSubstr)
			if err != nil {
				return
			}
			if string(got.conspub) != string(tt.want.conspub) || got.moniker != tt.want.moniker ||
				got.jailed != tt.want.jailed || got.bonded != tt.want.bonded {
				t.Errorf("getVal() = %+v, want %+v", got, tt.want)
			}
		})
	}
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

func TestGetConsensusAddress(t *testing.T) {
	key := testEd25519Key()
	tests := []struct {
		name      string
		pubkey    *codectypes.Any
		want      []byte
		errSubstr string
	}{
		{"nil pubkey", nil, nil, "missing consensus pubkey"},
		{"ed25519", mustAny(t, key), key.Address(), ""},
		{"bad ed25519 bytes", &codectypes.Any{TypeUrl: ed25519PubKeyTypeURL, Value: []byte{0xff}}, nil, "decoding ed25519"},
		{"short ed25519", mustAny(t, &ed25519.PubKey{Key: []byte{1, 2}}), nil, "expected 32"},
		{"bad secp256k1 bytes", &codectypes.Any{TypeUrl: secp256k1PubKeyTypeURL, Value: []byte{0xff}}, nil, "decoding secp256k1"},
		{"short secp256k1", mustAny(t, &secp256k1.PubKey{Key: []byte{1}}), nil, "expected 33"},
		{"unsupported type", &codectypes.Any{TypeUrl: "/cosmos.crypto.sr25519.PubKey"}, nil, "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getConsensusAddress(tt.pubkey)
			assertError(t, err, nil, tt.errSubstr)
			if string(got) != string(tt.want) {
				t.Errorf("getConsensusAddress() = %X, want %X", got, tt.want)
			}
		})
	}
}

func TestGetSigningInfo(t *testing.T) {
	tests := []struct {
		name      string
		result    fakeABCIResult
		wantInfo  slashing.ValidatorSigningInfo
		wantErr   error
		errSubstr string
	}{
		{"rpc error is not overwritten", errResult(errRateLimited), slashing.ValidatorSigningInfo{},
			errRateLimited, "could not query slashing status"},
		{"non-zero code", codeResult(22, "SigningInfo not found"), slashing.ValidatorSigningInfo{},
			errABCIQueryFailed, "SigningInfo not found"},
		{"nil response", fakeABCIResult{}, slashing.ValidatorSigningInfo{}, errEmptyABCIResponse, ""},
		{"garbage value", okResult([]byte{0xff, 0xff}), slashing.ValidatorSigningInfo{}, nil, "decoding signing info"},
		{"success", okResult(signingInfoValue(t, true, 42)),
			slashing.ValidatorSigningInfo{Tombstoned: true, MissedBlocksCounter: 42}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeABCIQuerier{results: map[string]fakeABCIResult{slashingSigningInfoPath: tt.result}}
			got, err := getSigningInfo(context.Background(), client, "cosmosvalcons1test")
			assertError(t, err, tt.wantErr, tt.errSubstr)
			if got.Tombstoned != tt.wantInfo.Tombstoned || got.MissedBlocksCounter != tt.wantInfo.MissedBlocksCounter {
				t.Errorf("getSigningInfo() = %+v, want %+v", got, tt.wantInfo)
			}
		})
	}
}

func TestGetSignedBlocksWindow(t *testing.T) {
	tests := []struct {
		name      string
		result    fakeABCIResult
		want      int64
		wantErr   error
		errSubstr string
	}{
		{"rpc error", errResult(errRateLimited), 0, errRateLimited, "could not query slashing params"},
		{"non-zero code", codeResult(1, "internal"), 0, errABCIQueryFailed, "internal"},
		{"empty value", okResult(nil), 0, errEmptyABCIResponse, ""},
		{"garbage value", okResult([]byte{0xff, 0xff}), 0, nil, "decoding slashing params"},
		{"success", okResult(paramsValue(t, 10000)), 10000, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeABCIQuerier{results: map[string]fakeABCIResult{slashingParamsPath: tt.result}}
			got, err := getSignedBlocksWindow(context.Background(), client)
			assertError(t, err, tt.wantErr, tt.errSubstr)
			if got != tt.want {
				t.Errorf("getSignedBlocksWindow() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDeriveValcons(t *testing.T) {
	address := make([]byte, consensusAddressLength)
	address[0] = 7
	tests := []struct {
		name      string
		valoper   string
		conspub   []byte
		want      string
		errSubstr string
	}{
		{"valcons passthrough", "cosmosvalcons1abc", nil, "cosmosvalcons1abc", ""},
		{"standard valoper", "cosmosvaloper1abc", address, mustBech32(t, "cosmosvalcons", address), ""},
		{"unknown prefix", "zzz1abc", address, "", "could not determine bech32 prefix"},
		{"short consensus address", "cosmosvaloper1abc", address[:4], "", "expected 20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := deriveValcons(tt.valoper, tt.conspub)
			assertError(t, err, nil, tt.errSubstr)
			if got != tt.want {
				t.Errorf("deriveValcons() = %q, want %q", got, tt.want)
			}
		})
	}
}

// knownGoodValInfo returns validator info as it would look after a successful refresh.
func knownGoodValInfo(conspub []byte, valcons string) ValInfo {
	return ValInfo{Moniker: "good", Bonded: true, Missed: 3, Window: 10000, Conspub: conspub, Valcons: valcons}
}

func TestUpdateValInfoKeepsLastKnownGood(t *testing.T) {
	key := testEd25519Key()
	valoper := mustBech32(t, "cosmosvaloper", make([]byte, consensusAddressLength))
	valcons := mustBech32(t, "cosmosvalcons", key.Address())
	bonded := okResult(validatorValue(t, "good", staking.Bonded, false, mustAny(t, key)))
	signing := okResult(signingInfoValue(t, false, 7))
	good := knownGoodValInfo(key.Address(), valcons)
	notConnected := ValInfo{Moniker: "not connected"}
	updatedSigning := good
	updatedSigning.Missed = 7

	tests := []struct {
		name    string
		prior   ValInfo
		results map[string]fakeABCIResult
		want    ValInfo
		wantErr error
	}{
		{"staking rpc error keeps prior", good,
			map[string]fakeABCIResult{stakingValidatorPath: errResult(errRateLimited)}, good, errRateLimited},
		{"staking code error keeps prior", good,
			map[string]fakeABCIResult{stakingValidatorPath: codeResult(5, "nope")}, good, errABCIQueryFailed},
		{"first run error stays not connected", notConnected,
			map[string]fakeABCIResult{stakingValidatorPath: errResult(errRateLimited)}, notConnected, errRateLimited},
		{"signing info error keeps prior slashing values", good, map[string]fakeABCIResult{
			stakingValidatorPath: bonded, slashingSigningInfoPath: errResult(errRateLimited)}, good, errRateLimited},
		{"params error on first run keeps window zero", notConnected, map[string]fakeABCIResult{
			stakingValidatorPath: bonded, slashingSigningInfoPath: signing, slashingParamsPath: errResult(errRateLimited)},
			ValInfo{Moniker: "good", Bonded: true, Conspub: key.Address(), Valcons: valcons}, errRateLimited},
		{"success with known window skips params", good, map[string]fakeABCIResult{
			stakingValidatorPath: bonded, slashingSigningInfoPath: signing}, updatedSigning, nil},
		{"success from first run", notConnected, map[string]fakeABCIResult{
			stakingValidatorPath: bonded, slashingSigningInfoPath: signing, slashingParamsPath: okResult(paramsValue(t, 10000))},
			updatedSigning, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prior := tt.prior
			cc := &ChainConfig{ChainId: "test-1", ValAddress: valoper, valInfo: &prior}
			err := cc.updateValInfo(context.Background(), &fakeABCIQuerier{results: tt.results}, false)
			assertError(t, err, tt.wantErr, "")
			assertValInfo(t, *cc.valInfo, tt.want)
		})
	}
}

func assertValInfo(t *testing.T, got, want ValInfo) {
	t.Helper()
	if got.Moniker != want.Moniker || got.Bonded != want.Bonded || got.Jailed != want.Jailed ||
		got.Tombstoned != want.Tombstoned || got.Missed != want.Missed || got.Window != want.Window ||
		got.Valcons != want.Valcons || string(got.Conspub) != string(want.Conspub) {
		t.Errorf("valInfo = %+v, want %+v", got, want)
	}
}

func TestGetValInfoNilClient(t *testing.T) {
	cc := &ChainConfig{ChainId: "test-1"}
	if err := cc.GetValInfo(false); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestToBytes(t *testing.T) {
	if got := ToBytes("0A0b"); string(got) != "\x0a\x0b" {
		t.Errorf("ToBytes(valid) = %X", got)
	}
	if got := ToBytes("zz"); got != nil {
		t.Errorf("ToBytes(invalid) = %X, want nil", got)
	}
}

func TestUpdateValInfoFirstRunPublishesMetrics(t *testing.T) {
	previousProm, previousStats := td.Prom, td.statsChan
	t.Cleanup(func() { td.Prom, td.statsChan = previousProm, previousStats })
	td.Prom = true
	td.statsChan = make(chan *promUpdate, 4)

	key := testEd25519Key()
	valoper := mustBech32(t, "cosmosvaloper", make([]byte, consensusAddressLength))
	client := &fakeABCIQuerier{results: map[string]fakeABCIResult{
		stakingValidatorPath:    okResult(validatorValue(t, "inactive", staking.Unbonded, false, mustAny(t, key))),
		slashingSigningInfoPath: okResult(signingInfoValue(t, true, 9)),
		slashingParamsPath:      okResult(paramsValue(t, 500)),
	}}
	cc := &ChainConfig{ChainId: "test-1", ValAddress: valoper, valInfo: &ValInfo{Moniker: "not connected"}}
	if err := cc.updateValInfo(context.Background(), client, true); err != nil {
		t.Fatalf("updateValInfo() error = %v", err)
	}
	if !cc.valInfo.Tombstoned || cc.valInfo.Bonded || cc.valInfo.Window != 500 {
		t.Errorf("valInfo = %+v, want tombstoned, unbonded, window 500", cc.valInfo)
	}
	want := []metricType{metricWindowMissed, metricWindowSize, metricTotalNodes}
	for _, metric := range want {
		update := <-td.statsChan
		if update.metric != metric {
			t.Errorf("metric = %v, want %v", update.metric, metric)
		}
	}
}

func TestSnapshotValInfoNil(t *testing.T) {
	cc := &ChainConfig{}
	if got := cc.snapshotValInfo(); got.Moniker != "" || got.Conspub != nil {
		t.Errorf("snapshotValInfo() on nil = %+v, want zero value", got)
	}
}

func TestUpdateValInfoUnknownPrefixKeepsPrior(t *testing.T) {
	key := testEd25519Key()
	client := &fakeABCIQuerier{results: map[string]fakeABCIResult{
		stakingValidatorPath: okResult(validatorValue(t, "other", staking.Bonded, false, mustAny(t, key))),
	}}
	prior := knownGoodValInfo(key.Address(), "zzzcons1prior")
	cc := &ChainConfig{ChainId: "test-1", ValAddress: "zzz1unknownprefix", valInfo: &prior}
	err := cc.updateValInfo(context.Background(), client, false)
	assertError(t, err, nil, "could not determine bech32 prefix")
	assertValInfo(t, *cc.valInfo, knownGoodValInfo(key.Address(), "zzzcons1prior"))
}
