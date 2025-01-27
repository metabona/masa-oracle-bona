package workers

import (
	"math/rand/v2"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sirupsen/logrus"

	masa "github.com/masa-finance/masa-oracle/pkg"
	"github.com/masa-finance/masa-oracle/pkg/pubsub"
	data_types "github.com/masa-finance/masa-oracle/pkg/workers/types"
)

// GetEligibleWorkers Uses the new NodeTracker method to get the eligible workers for a given message type
// I'm leaving this returning an array so that we can easily increase the number of workers in the future
func GetEligibleWorkers(node *masa.OracleNode, category pubsub.WorkerCategory) ([]data_types.Worker, *data_types.Worker) {
	workers := make([]data_types.Worker, 0)
	nodes := node.NodeTracker.GetEligibleWorkerNodes(category)
	var localWorker *data_types.Worker

	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	logrus.Info("Getting eligible workers")
	for _, eligible := range nodes {
		if eligible.PeerId.String() == node.Host.ID().String() {
			localAddrInfo := peer.AddrInfo{
				ID:    node.Host.ID(),
				Addrs: node.Host.Addrs(),
			}
			localWorker = &data_types.Worker{IsLocal: true, NodeData: eligible, AddrInfo: &localAddrInfo}
			continue
		}
		workers = append(workers, data_types.Worker{IsLocal: false, NodeData: eligible})
	}

	logrus.Infof("Found %d eligible remote workers", len(workers))
	return workers, localWorker
}
