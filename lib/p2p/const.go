package p2p

import (
	"time"

	"github.com/djylb/nps/lib/common"
)

const (
	// server exchange
	p2pServerWaitTimeout        = 30 * time.Second
	p2pServerReadStep           = 1 * time.Second
	p2pServerCollectMoreTimeout = 8 * time.Second

	// handshake read loop
	p2pHandshakeReadMax = 1500 * time.Millisecond
	p2pHandshakeTimeout = 20

	// base send
	p2pConeSendTick      = 500 * time.Millisecond
	p2pConeMultiSendTick = 1200 * time.Millisecond
	p2pConeBurstCount    = 3
	p2pConeBurstGap      = 80 * time.Millisecond
	p2pLowTTLBurst       = 3
	p2pLowTTLGAP         = 20 * time.Millisecond
	p2pLowTTLValue       = 3
	p2pLowTTLPause       = 150 * time.Millisecond
	p2pDefaultTTL        = 64
	p2pDefaultHopLimit   = 64

	// near scan (regular ports change)
	p2pConeNearScanCount       = 128
	p2pTargetSprayRounds       = 2
	p2pTargetSprayBurst        = 16
	p2pTargetSprayInterval     = 3 * time.Millisecond
	p2pTargetSprayPhaseGap     = 40 * time.Millisecond
	p2pConeNearScanRange       = 256
	p2pConeNearScanTick        = 1500 * time.Millisecond
	p2pConeSmallContigRange    = 6
	p2pConeSmallContigSendTick = 1200 * time.Millisecond

	// heavy random scan fallback
	p2pConeFallbackDelay = 1800 * time.Millisecond
	p2pConeFallbackCount = 512
	p2pConeFallbackTick  = 2 * time.Second

	// extra listen ports when self seems symmetric-ish (receiver-like)
	p2pSelfHardExtraListenCount = 128

	// handshake budgets / throttling
	p2pSuccMinInterval = 200 * time.Millisecond
	p2pEndMinInterval  = 200 * time.Millisecond

	p2pSuccBurstOnConnect = 4
	p2pSuccEchoOnSuccess  = 2
	p2pEndBurstOnSuccess  = 6
	p2pEndBurstOnEndAck   = 2

	p2pMaxSuccPacketsPerPeer = 20
	p2pMaxEndPacketsPerPeer  = 20

	p2pSprayTick       = 200 * time.Millisecond
	p2pSpraySuccMax    = 6
	p2pSprayEndMax     = 6
	p2pSpraySuccWindow = 1600 * time.Millisecond
	p2pSprayEndWindow  = 1600 * time.Millisecond
	p2pSpraySeedSucc   = 2
	p2pSpraySeedWindow = 900 * time.Millisecond
)

var (
	bConnDataSeq = []byte(common.CONN_DATA_SEQ)
	bConnect     = []byte(common.WORK_P2P_CONNECT)
	bSuccess     = []byte(common.WORK_P2P_SUCCESS)
	bEnd         = []byte(common.WORK_P2P_END)
)
