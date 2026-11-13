package tenderduty

import (
	"context"
	"errors"
	"fmt"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

// ABCI query paths used to read validator state from the staking and slashing modules.
const (
	stakingValidatorPath    = "/cosmos.staking.v1beta1.Query/Validator"
	slashingSigningInfoPath = "/cosmos.slashing.v1beta1.Query/SigningInfo"
	slashingParamsPath      = "/cosmos.slashing.v1beta1.Query/Params"
)

// abciCodeOK is the ABCI response code for a successful query.
const abciCodeOK uint32 = 0

var (
	// errEmptyABCIResponse is returned when a node answers an ABCI query without a value.
	errEmptyABCIResponse = errors.New("empty ABCI query response")
	// errABCIQueryFailed is returned when a node answers an ABCI query with a non-zero code.
	errABCIQueryFailed = errors.New("ABCI query failed")
)

// abciQuerier is the subset of the tendermint RPC client used for validator queries.
// It is satisfied by *rpchttp.HTTP and allows tests to substitute a fake.
type abciQuerier interface {
	ABCIQuery(ctx context.Context, path string, data tmbytes.HexBytes) (*ctypes.ResultABCIQuery, error)
}

// queryABCI performs an ABCI query and returns the raw response value. It reports transport
// errors, application errors (non-zero code) and empty responses as distinct wrapped errors.
func queryABCI(ctx context.Context, client abciQuerier, path string, request []byte) ([]byte, error) {
	response, err := client.ABCIQuery(ctx, path, request)
	if err != nil {
		return nil, fmt.Errorf("abci query %s: %w", path, err)
	}
	if response == nil {
		return nil, fmt.Errorf("abci query %s: %w", path, errEmptyABCIResponse)
	}
	result := response.Response
	if result.Code != abciCodeOK {
		return nil, fmt.Errorf("abci query %s: %w: code %d (codespace %q): %s",
			path, errABCIQueryFailed, result.Code, result.Codespace, result.Log)
	}
	if result.Value == nil {
		return nil, fmt.Errorf("abci query %s: %w", path, errEmptyABCIResponse)
	}
	return result.Value, nil
}
