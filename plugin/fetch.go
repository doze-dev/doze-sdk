package plugin

import (
	"context"
	"errors"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/plugin/proto"
)

// Engine binaries are fetched by the host, never by a module: the host owns
// the mirror, the cache and their checks, so a change to any of them needs no
// module rebuilt. During a Resolve the host serves its engine.Fetcher on a
// go-plugin broker id, and the plugin's driver fetches through a client for it.

// errOldHost is a fetch asked of a doze that predates host fetching.
var errOldHost = errors.New("this doze is older than the module and can't download engine binaries for it: upgrade doze")

// oldHostFetcher is the Fetcher a module gets from such a doze. A driver that
// downloads nothing (aws, kafka) never notices; one that does is told why.
type oldHostFetcher struct{}

func (oldHostFetcher) ResolveMajor(string, string) (string, error) { return "", errOldHost }
func (oldHostFetcher) Ensure(context.Context, string, string, engine.Platform, string) (string, string, error) {
	return "", "", errOldHost
}

// serveFetcher serves f on a new broker id until the returned stop is called.
func serveFetcher(broker *goplugin.GRPCBroker, f engine.Fetcher) (id uint32, stop func(), err error) {
	if broker == nil || f == nil {
		return 0, func() {}, nil
	}
	id = broker.NextId()
	l, err := broker.Accept(id)
	if err != nil {
		return 0, nil, err
	}
	srv := grpc.NewServer()
	proto.RegisterFetcherServer(srv, fetcherServer{f: f})
	go func() { _ = srv.Serve(l) }()
	return id, srv.Stop, nil
}

// fetcherServer is the host's engine.Fetcher on the wire.
type fetcherServer struct {
	proto.UnimplementedFetcherServer
	f engine.Fetcher
}

func (s fetcherServer) ResolveMajor(_ context.Context, req *proto.ResolveMajorRequest) (*proto.ResolveMajorResponse, error) {
	full, err := s.f.ResolveMajor(req.Engine, req.Major)
	if err != nil {
		return nil, err
	}
	return &proto.ResolveMajorResponse{Full: full}, nil
}

func (s fetcherServer) Ensure(ctx context.Context, req *proto.EnsureRequest) (*proto.EnsureResponse, error) {
	bin, digest, err := s.f.Ensure(ctx, req.Engine, req.Full, platformFromProto(req.Platform), req.ExpectedSha)
	if err != nil {
		return nil, err
	}
	return &proto.EnsureResponse{BinDir: bin, Digest: digest}, nil
}

// fetcherClient is the plugin's engine.Fetcher: the host's, across the broker.
type fetcherClient struct {
	ctx context.Context
	c   proto.FetcherClient
}

func (c fetcherClient) ResolveMajor(engineType, major string) (string, error) {
	resp, err := c.c.ResolveMajor(c.ctx, &proto.ResolveMajorRequest{Engine: engineType, Major: major})
	if err != nil {
		return "", userError(err)
	}
	return resp.Full, nil
}

func (c fetcherClient) Ensure(ctx context.Context, engineType, full string, plat engine.Platform, expectedSHA string) (string, string, error) {
	resp, err := c.c.Ensure(ctx, &proto.EnsureRequest{Engine: engineType, Full: full, Platform: platformToProto(plat), ExpectedSha: expectedSHA})
	if err != nil {
		return "", "", userError(err)
	}
	return resp.BinDir, resp.Digest, nil
}

// dialFetcher connects to the host's Fetcher named in a ResolveRequest. It
// is valid for that Resolve only: the host stops serving it when Resolve
// returns.
func dialFetcher(ctx context.Context, broker *goplugin.GRPCBroker, id uint32) (engine.Fetcher, func(), error) {
	if id == 0 || broker == nil {
		return oldHostFetcher{}, func() {}, nil
	}
	conn, err := broker.Dial(id)
	if err != nil {
		return nil, nil, fmt.Errorf("reaching doze to download engine binaries: %w", err)
	}
	return fetcherClient{ctx: ctx, c: proto.NewFetcherClient(conn)}, func() { _ = conn.Close() }, nil
}
