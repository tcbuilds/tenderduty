package tenderduty

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	slashing "github.com/cosmos/cosmos-sdk/x/slashing/types"
	staking "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// valInfoTimeout bounds the total time spent on the queries performed by GetValInfo.
const valInfoTimeout = 10 * time.Second

// consensusAddressLength is the size in bytes of a tendermint consensus address.
const consensusAddressLength = 20

// Protobuf type URLs of the supported consensus public keys.
const (
	ed25519PubKeyTypeURL   = "/cosmos.crypto.ed25519.PubKey"
	secp256k1PubKeyTypeURL = "/cosmos.crypto.secp256k1.PubKey"
)

// ValInfo holds most of the stats/info used for secondary alarms. It is refreshed roughly every minute.
type ValInfo struct {
	Moniker    string `json:"moniker"`
	Bonded     bool   `json:"bonded"`
	Jailed     bool   `json:"jailed"`
	Tombstoned bool   `json:"tombstoned"`
	Missed     int64  `json:"missed"`
	Window     int64  `json:"window"`
	Conspub    []byte `json:"conspub"`
	Valcons    string `json:"valcons"`
}

// stakingValidator is the subset of the staking module's validator record used by tenderduty.
type stakingValidator struct {
	conspub []byte
	moniker string
	jailed  bool
	bonded  bool
}

// GetValInfo refreshes the validator's staking and slashing state, the first bool is used to determine if extra
// information about the validator should be printed. Each stage only replaces cc.valInfo after all of its queries
// succeed, so a transient RPC failure keeps the last known good values instead of making an active validator look
// inactive (which would also disable missed block tracking).
func (cc *ChainConfig) GetValInfo(first bool) error {
	cc.mtx.RLock()
	client := cc.client
	cc.mtx.RUnlock()
	if client == nil {
		return errors.New("nil rpc client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), valInfoTimeout)
	defer cancel()

	if err := cc.updateValInfo(ctx, client, first); err != nil {
		return fmt.Errorf("%s via %s: %w", cc.ChainId, client.Remote(), err)
	}
	return nil
}

// updateValInfo runs the staking and slashing refresh stages against client and publishes the result.
func (cc *ChainConfig) updateValInfo(ctx context.Context, client abciQuerier, first bool) error {
	if err := cc.refreshStakingInfo(ctx, client, first); err != nil {
		return err
	}
	if err := cc.refreshSlashingInfo(ctx, client); err != nil {
		return err
	}
	cc.publishValInfo(first)
	return nil
}

// refreshStakingInfo updates the moniker, bond/jail status and consensus address from the staking module.
func (cc *ChainConfig) refreshStakingInfo(ctx context.Context, client abciQuerier, first bool) error {
	validator, err := getVal(ctx, client, cc.ValAddress)
	if err != nil {
		return err
	}
	valcons, err := deriveValcons(cc.ValAddress, validator.conspub)
	if err != nil {
		return err
	}
	next := cc.snapshotValInfo()
	next.Conspub = validator.conspub
	next.Moniker = validator.moniker
	next.Jailed = validator.jailed
	next.Bonded = validator.bonded
	next.Valcons = valcons
	cc.storeValInfo(next)

	if first {
		cc.logStakingInfo(next)
	}
	return nil
}

// refreshSlashingInfo updates the tombstone status, missed block counter and signed blocks window.
func (cc *ChainConfig) refreshSlashingInfo(ctx context.Context, client abciQuerier) error {
	next := cc.snapshotValInfo()
	signing, err := getSigningInfo(ctx, client, next.Valcons)
	if err != nil {
		return err
	}
	if next.Window == 0 {
		next.Window, err = getSignedBlocksWindow(ctx, client)
		if err != nil {
			return err
		}
	}
	next.Tombstoned = signing.Tombstoned
	next.Missed = signing.MissedBlocksCounter
	cc.storeValInfo(next)
	return nil
}

// snapshotValInfo returns a copy of the current validator info that is safe to modify.
func (cc *ChainConfig) snapshotValInfo() ValInfo {
	cc.mtx.RLock()
	defer cc.mtx.RUnlock()
	if cc.valInfo == nil {
		return ValInfo{}
	}
	return *cc.valInfo
}

// storeValInfo replaces the current validator info with a fully populated value.
func (cc *ChainConfig) storeValInfo(next ValInfo) {
	cc.mtx.Lock()
	cc.valInfo = &next
	cc.mtx.Unlock()
}

// logStakingInfo prints the validator's status and consensus address on the first lookup.
func (cc *ChainConfig) logStakingInfo(info ValInfo) {
	if info.Bonded {
		l(fmt.Sprintf("⚙️ found %s (%s) in validator set", cc.ValAddress, info.Moniker))
	} else {
		l(fmt.Sprintf("❌ %s (%s) is INACTIVE", cc.ValAddress, info.Moniker))
	}
	if info.Valcons != cc.ValAddress {
		l("⚙️", cc.ValAddress, "is using consensus key:", info.Valcons)
	}
}

// publishValInfo logs a tombstoned validator and sends the window metrics to prometheus.
func (cc *ChainConfig) publishValInfo(first bool) {
	info := cc.snapshotValInfo()
	if info.Tombstoned {
		l(fmt.Sprintf("❗️☠️ %s (%s) is tombstoned 🪦❗️", cc.ValAddress, info.Moniker))
	}
	if !td.Prom {
		return
	}
	td.statsChan <- cc.mkUpdate(metricWindowMissed, float64(info.Missed), "")
	if first {
		td.statsChan <- cc.mkUpdate(metricWindowSize, float64(info.Window), "")
		td.statsChan <- cc.mkUpdate(metricTotalNodes, float64(len(cc.Nodes)), "")
	}
}

// deriveValcons returns the bech32 consensus address for a validator. Operator addresses are converted using the
// chain's consensus prefix, consensus addresses are returned unchanged.
func deriveValcons(valAddress string, conspub []byte) (string, error) {
	if strings.Contains(valAddress, "valcons") {
		return valAddress, nil
	}
	if len(conspub) < consensusAddressLength {
		return "", fmt.Errorf("consensus address for %s has %d bytes, expected %d",
			valAddress, len(conspub), consensusAddressLength)
	}
	prefix, err := getValconsPrefix(valAddress)
	if err != nil {
		return "", err
	}
	valcons, err := bech32.ConvertAndEncode(prefix, conspub[:consensusAddressLength])
	if err != nil {
		return "", fmt.Errorf("encoding %s consensus address for %s: %w", prefix, valAddress, err)
	}
	return valcons, nil
}

// getValconsPrefix determines the consensus address prefix for a valoper address, this is fragile: chains that
// don't follow the "<prefix>valoper" convention need an entry in altValopers.
func getValconsPrefix(valoper string) (string, error) {
	split := strings.Split(valoper, "valoper")
	if len(split) == 2 {
		return split[0] + "valcons", nil
	}
	if prefix, ok := altValopers.getAltPrefix(valoper); ok {
		return prefix, nil
	}
	return "", errors.New("❓ could not determine bech32 prefix from valoper address: " + valoper)
}

// getVal queries the staking module for the validator's consensus address, moniker, and jail/bond status.
func getVal(ctx context.Context, client abciQuerier, valoper string) (stakingValidator, error) {
	if strings.Contains(valoper, "valcons") {
		_, consensusAddress, err := bech32.DecodeAndConvert(valoper)
		if err != nil {
			return stakingValidator{}, fmt.Errorf("could not decode consensus address %s: %w", valoper, err)
		}
		return stakingValidator{conspub: consensusAddress, moniker: valoper, bonded: true}, nil
	}

	request := staking.QueryValidatorRequest{ValidatorAddr: valoper}
	payload, err := request.Marshal()
	if err != nil {
		return stakingValidator{}, fmt.Errorf("encoding validator query for %s: %w", valoper, err)
	}
	value, err := queryABCI(ctx, client, stakingValidatorPath, payload)
	if err != nil {
		return stakingValidator{}, fmt.Errorf("querying validator %s: %w", valoper, err)
	}
	response := &staking.QueryValidatorResponse{}
	if err = response.Unmarshal(value); err != nil {
		return stakingValidator{}, fmt.Errorf("decoding validator %s: %w", valoper, err)
	}
	conspub, err := getConsensusAddress(response.Validator.ConsensusPubkey)
	if err != nil {
		return stakingValidator{}, fmt.Errorf("validator %s: %w", valoper, err)
	}
	return stakingValidator{
		conspub: conspub,
		moniker: response.Validator.GetMoniker(),
		jailed:  response.Validator.Jailed,
		bonded:  response.Validator.Status == staking.Bonded,
	}, nil
}

// getConsensusAddress converts a validator's consensus public key into its consensus address bytes.
func getConsensusAddress(pubkey *codectypes.Any) ([]byte, error) {
	if pubkey == nil {
		return nil, errors.New("missing consensus pubkey")
	}
	switch pubkey.TypeUrl {
	case ed25519PubKeyTypeURL:
		key := ed25519.PubKey{}
		if err := key.Unmarshal(pubkey.Value); err != nil {
			return nil, fmt.Errorf("decoding ed25519 consensus pubkey: %w", err)
		}
		if len(key.Key) != ed25519.PubKeySize {
			return nil, fmt.Errorf("ed25519 consensus pubkey has %d bytes, expected %d", len(key.Key), ed25519.PubKeySize)
		}
		return key.Address().Bytes(), nil
	case secp256k1PubKeyTypeURL:
		key := secp256k1.PubKey{}
		if err := key.Unmarshal(pubkey.Value); err != nil {
			return nil, fmt.Errorf("decoding secp256k1 consensus pubkey: %w", err)
		}
		if len(key.Key) != secp256k1.PubKeySize {
			return nil, fmt.Errorf("secp256k1 consensus pubkey has %d bytes, expected %d", len(key.Key), secp256k1.PubKeySize)
		}
		return key.Address().Bytes(), nil
	}
	return nil, fmt.Errorf("unsupported consensus pubkey type %q", pubkey.TypeUrl)
}

// getSigningInfo queries the slashing module for the validator's signing info (tombstone status, missed blocks).
func getSigningInfo(ctx context.Context, client abciQuerier, valcons string) (slashing.ValidatorSigningInfo, error) {
	request := slashing.QuerySigningInfoRequest{ConsAddress: valcons}
	payload, err := request.Marshal()
	if err != nil {
		return slashing.ValidatorSigningInfo{}, fmt.Errorf("encoding signing info query for %s: %w", valcons, err)
	}
	value, err := queryABCI(ctx, client, slashingSigningInfoPath, payload)
	if err != nil {
		return slashing.ValidatorSigningInfo{}, fmt.Errorf("could not query slashing status for %s: %w", valcons, err)
	}
	response := &slashing.QuerySigningInfoResponse{}
	if err = response.Unmarshal(value); err != nil {
		return slashing.ValidatorSigningInfo{}, fmt.Errorf("decoding signing info for %s: %w", valcons, err)
	}
	return response.ValSigningInfo, nil
}

// getSignedBlocksWindow queries the slashing module parameters for the signed blocks window.
func getSignedBlocksWindow(ctx context.Context, client abciQuerier) (int64, error) {
	request := &slashing.QueryParamsRequest{}
	payload, err := request.Marshal()
	if err != nil {
		return 0, fmt.Errorf("encoding slashing params query: %w", err)
	}
	value, err := queryABCI(ctx, client, slashingParamsPath, payload)
	if err != nil {
		return 0, fmt.Errorf("🛑 could not query slashing params: %w", err)
	}
	params := &slashing.QueryParamsResponse{}
	if err = params.Unmarshal(value); err != nil {
		return 0, fmt.Errorf("decoding slashing params: %w", err)
	}
	return params.Params.SignedBlocksWindow, nil
}

// ToBytes decodes a hex string, returning nil if it is not valid hex.
func ToBytes(address string) []byte {
	bz, err := hex.DecodeString(strings.ToLower(address))
	if err != nil {
		return nil
	}
	return bz
}
