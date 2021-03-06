package node

import (
	"context"

	"github.com/ipfs/fs-repo-migrations/ipfs-10-to-11/_vendor/github.com/ipfs/go-ipfs/peering"
	"github.com/ipfs/fs-repo-migrations/ipfs-10-to-11/_vendor/github.com/libp2p/go-libp2p-core/host"
	"github.com/ipfs/fs-repo-migrations/ipfs-10-to-11/_vendor/github.com/libp2p/go-libp2p-core/peer"
	"github.com/ipfs/fs-repo-migrations/ipfs-10-to-11/_vendor/go.uber.org/fx"
)

// Peering constructs the peering service and hooks it into fx's lifetime
// management system.
func Peering(lc fx.Lifecycle, host host.Host) *peering.PeeringService {
	ps := peering.NewPeeringService(host)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			return ps.Start()
		},
		OnStop: func(context.Context) error {
			return ps.Stop()
		},
	})
	return ps
}

// PeerWith configures the peering service to peer with the specified peers.
func PeerWith(peers ...peer.AddrInfo) fx.Option {
	return fx.Invoke(func(ps *peering.PeeringService) {
		for _, ai := range peers {
			ps.AddPeer(ai)
		}
	})
}
