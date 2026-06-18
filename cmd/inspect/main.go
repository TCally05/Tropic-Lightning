// Command inspect is a small operator CLI for a peat node's PeatSidecar gRPC
// API. The portal itself only does document CRUD + GetStatus; this tool covers
// the *peering* surface (ConnectPeer/ListPeers/StartSync/...) so you can wire and
// verify a peat mesh by hand.
//
// peat peers over Iroh (QUIC/UDP, public-key endpoint IDs, relay-assisted NAT
// traversal). To connect two nodes you point this at one node's sidecar and give
// it the *other* node's Iroh endpoint ID plus a direct address and/or relay URL,
// then StartSync on both.
//
// Usage:
//
//	go run ./cmd/inspect status      --addr HOST:50051
//	go run ./cmd/inspect peers       --addr HOST:50051
//	go run ./cmd/inspect connect     --addr HOST:50051 --endpoint-id <hex> [--peer-addr host:port ...] [--relay URL]
//	go run ./cmd/inspect start-sync  --addr HOST:50051
//	go run ./cmd/inspect stop-sync   --addr HOST:50051
//	go run ./cmd/inspect sync-stats  --addr HOST:50051
//
// --addr defaults to $PEAT_NODE_ADDR or localhost:50051. Add --tls to dial TLS.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	sidecarv1 "github.com/defenseunicorns/keycloak-portal/internal/peat/sidecarv1"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	addr := fs.String("addr", envOr("PEAT_NODE_ADDR", "localhost:50051"), "peat sidecar gRPC address (host:port)")
	useTLS := fs.Bool("tls", false, "dial the sidecar over TLS")

	// connect-only flags
	var endpointID, relay string
	var peerAddrs multiFlag
	if cmd == "connect" {
		fs.StringVar(&endpointID, "endpoint-id", "", "remote node's hex Iroh endpoint ID (required)")
		fs.StringVar(&relay, "relay", "", "remote node's relay URL (optional)")
		fs.Var(&peerAddrs, "peer-addr", "remote node direct address host:port (repeatable)")
	}
	_ = fs.Parse(os.Args[2:])

	client, closeConn, err := dial(*addr, *useTLS)
	if err != nil {
		fatalf("dial %s: %v", *addr, err)
	}
	defer closeConn()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch cmd {
	case "status":
		st, err := client.GetStatus(ctx, &sidecarv1.GetStatusRequest{})
		check(err)
		fmt.Printf("node_id:         %s\n", st.NodeId)
		fmt.Printf("endpoint_addr:   %s\n", st.EndpointAddr)
		fmt.Printf("phase:           %s\n", st.Phase)
		fmt.Printf("sync_active:     %v\n", st.SyncActive)
		fmt.Printf("connected_peers: %d\n", st.ConnectedPeers)

	case "peers":
		resp, err := client.ListPeers(ctx, &sidecarv1.ListPeersRequest{})
		check(err)
		if len(resp.Peers) == 0 {
			fmt.Println("no peers")
			return
		}
		for _, p := range resp.Peers {
			fmt.Printf("- %s  connected=%v  addrs=%s\n", p.EndpointId, p.Connected, strings.Join(p.Addresses, ","))
		}

	case "connect":
		if endpointID == "" {
			fatalf("--endpoint-id is required (the remote node's hex Iroh endpoint ID; see `inspect status` on that node)")
		}
		_, err := client.ConnectPeer(ctx, &sidecarv1.ConnectPeerRequest{
			EndpointId: endpointID,
			Addresses:  peerAddrs,
			RelayUrl:   relay,
		})
		check(err)
		fmt.Printf("connect requested for %s (addrs=%s relay=%s)\n", endpointID, strings.Join(peerAddrs, ","), relay)
		fmt.Println("run `start-sync` on both nodes, then check `peers` / `sync-stats`")

	case "start-sync":
		_, err := client.StartSync(ctx, &sidecarv1.StartSyncRequest{})
		check(err)
		fmt.Println("sync started")

	case "stop-sync":
		_, err := client.StopSync(ctx, &sidecarv1.StopSyncRequest{})
		check(err)
		fmt.Println("sync stopped")

	case "sync-stats":
		s, err := client.GetSyncStats(ctx, &sidecarv1.GetSyncStatsRequest{})
		check(err)
		fmt.Printf("sync_active:     %v\n", s.SyncActive)
		fmt.Printf("connected_peers: %d\n", s.ConnectedPeers)
		fmt.Printf("bytes_sent:      %d\n", s.BytesSent)
		fmt.Printf("bytes_received:  %d\n", s.BytesReceived)

	default:
		usage()
		os.Exit(2)
	}
}

func dial(addr string, useTLS bool) (sidecarv1.PeatSidecarClient, func(), error) {
	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if useTLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, nil, err
	}
	return sidecarv1.NewPeatSidecarClient(conn), func() { _ = conn.Close() }, nil
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func check(err error) {
	if err != nil {
		fatalf("rpc error: %v", err)
	}
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `inspect — peat node operator CLI

Commands:
  status       show node id, endpoint addr, phase, peer count
  peers        list known/connected peers
  connect      connect to a peer (--endpoint-id, optional --peer-addr / --relay)
  start-sync   begin CRDT synchronization
  stop-sync    pause synchronization
  sync-stats   show sync stats (bytes in/out, peers)

Common flags: --addr HOST:50051 (or $PEAT_NODE_ADDR), --tls`)
}
