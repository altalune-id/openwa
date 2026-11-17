package boot

import (
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
)

// NOTE: the queue's streams reserve 576 MiB; without a fixed cap JetStream sizes the store from free host disk, so a full laptop disk failed boot tests.
const natsTestMaxStore = 2 << 30

func startNATSServer(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, JetStreamMaxStore: natsTestMaxStore, StoreDir: t.TempDir(), NoLog: true, NoSigs: true}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)
	return ns
}

func TestStartNATSServer_StoreLimitIgnoresHostDisk(t *testing.T) {
	ns := startNATSServer(t)
	require.Equal(t, int64(natsTestMaxStore), ns.JetStreamConfig().MaxStore)
}
