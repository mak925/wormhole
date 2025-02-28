package processor

import (
	"context"
	"crypto/ecdsa"
	"time"

	"github.com/certusone/wormhole/node/pkg/notify/discord"

	"github.com/certusone/wormhole/node/pkg/db"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"go.uber.org/zap"

	"github.com/certusone/wormhole/node/pkg/common"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	"github.com/certusone/wormhole/node/pkg/reporter"
	"github.com/certusone/wormhole/node/pkg/supervisor"
	"github.com/certusone/wormhole/node/pkg/vaa"
)

type (
	// vaaState represents the local view of a given VAA
	vaaState struct {
		// First time this digest was seen (possibly even before we observed it ourselves).
		firstObserved time.Time
		// Copy of the VAA we constructed when we made our own observation.
		ourVAA *vaa.VAA
		// Map of signatures seen by guardian. During guardian set updates, this may contain signatures belonging
		// to either the old or new guardian set.
		signatures map[ethcommon.Address][]byte
		// Flag set after reaching quorum and submitting the VAA.
		submitted bool
		// Flag set by the cleanup service after the settlement timeout has expired and misses were counted.
		settled bool
		// Human-readable description of the VAA's source, used for metrics.
		source string
		// Number of times the cleanup service has attempted to retransmit this VAA.
		retryCount uint
		// Copy of the bytes we submitted (ourVAA, but signed and serialized). Used for retransmissions.
		ourMsg []byte
		// Copy of the guardian set valid at observation/injection time.
		gs *common.GuardianSet
	}

	vaaMap map[string]*vaaState

	// aggregationState represents the node's aggregation of guardian signatures.
	aggregationState struct {
		vaaSignatures vaaMap
	}
)

type Processor struct {
	// lockC is a channel of observed emitted messages
	lockC chan *common.MessagePublication
	// setC is a channel of guardian set updates
	setC chan *common.GuardianSet

	// sendC is a channel of outbound messages to broadcast on p2p
	sendC chan []byte
	// obsvC is a channel of inbound decoded observations from p2p
	obsvC chan *gossipv1.SignedObservation
	// signedInC is a channel of inbound signed VAA observations from p2p
	signedInC chan *gossipv1.SignedVAAWithQuorum

	// injectC is a channel of VAAs injected locally.
	injectC chan *vaa.VAA

	// gk is the node's guardian private key
	gk *ecdsa.PrivateKey

	// devnetMode specified whether to submit transactions to the hardcoded Ethereum devnet
	devnetMode         bool
	devnetNumGuardians uint
	devnetEthRPC       string

	attestationEvents *reporter.AttestationEventReporter

	logger *zap.Logger

	db *db.Database

	// Runtime state

	// gs is the currently valid guardian set
	gs *common.GuardianSet
	// gst is managed by the processor and allows concurrent access to the
	// guardian set by other components.
	gst *common.GuardianSetState

	// state is the current runtime VAA view
	state *aggregationState
	// gk pk as eth address
	ourAddr ethcommon.Address
	// cleanup triggers periodic state cleanup
	cleanup *time.Ticker

	notifier *discord.DiscordNotifier
}

func NewProcessor(
	ctx context.Context,
	db *db.Database,
	lockC chan *common.MessagePublication,
	setC chan *common.GuardianSet,
	sendC chan []byte,
	obsvC chan *gossipv1.SignedObservation,
	injectC chan *vaa.VAA,
	signedInC chan *gossipv1.SignedVAAWithQuorum,
	gk *ecdsa.PrivateKey,
	gst *common.GuardianSetState,
	devnetMode bool,
	devnetNumGuardians uint,
	devnetEthRPC string,
	attestationEvents *reporter.AttestationEventReporter,
	notifier *discord.DiscordNotifier,
) *Processor {

	return &Processor{
		lockC:              lockC,
		setC:               setC,
		sendC:              sendC,
		obsvC:              obsvC,
		signedInC:          signedInC,
		injectC:            injectC,
		gk:                 gk,
		gst:                gst,
		devnetMode:         devnetMode,
		devnetNumGuardians: devnetNumGuardians,
		devnetEthRPC:       devnetEthRPC,
		db:                 db,

		attestationEvents: attestationEvents,

		notifier: notifier,

		logger:  supervisor.Logger(ctx),
		state:   &aggregationState{vaaMap{}},
		ourAddr: crypto.PubkeyToAddress(gk.PublicKey),
	}
}

func (p *Processor) Run(ctx context.Context) error {
	p.cleanup = time.NewTicker(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p.gs = <-p.setC:
			p.logger.Info("guardian set updated",
				zap.Strings("set", p.gs.KeysAsHexStrings()),
				zap.Uint32("index", p.gs.Index))
			p.gst.Set(p.gs)
		case k := <-p.lockC:
			p.handleMessage(ctx, k)
		case v := <-p.injectC:
			p.handleInjection(ctx, v)
		case m := <-p.obsvC:
			p.handleObservation(ctx, m)
		case m := <-p.signedInC:
			p.handleInboundSignedVAAWithQuorum(ctx, m)
		case <-p.cleanup.C:
			p.handleCleanup(ctx)
		}
	}
}
