// Package server implements a Bazel Build Event Service (BES) backend:
// the gRPC service Bazel streams build events to via --bes_backend.
package server

import (
	"context"
	"io"
	"log"
	"sort"
	"time"

	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
	"github.com/nalajala4naresh/bepper/src/index"
	"github.com/nalajala4naresh/bepper/src/store"

	besv1 "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// BuildEventServer implements besv1.PublishBuildEventServer.
type BuildEventServer struct {
	besv1.UnimplementedPublishBuildEventServer

	store *store.Store
	idx   index.Indexer
}

// NewBuildEventServer creates a BuildEventServer that persists events via s
// and indexes invocation summaries via idx.
func NewBuildEventServer(s *store.Store, idx index.Indexer) *BuildEventServer {
	return &BuildEventServer{store: s, idx: idx}
}

// Register wires the server into a grpc.Server.
func (s *BuildEventServer) Register(grpcServer *grpc.Server) {
	besv1.RegisterPublishBuildEventServer(grpcServer, s)
}

func (s *BuildEventServer) PublishLifecycleEvent(ctx context.Context, req *besv1.PublishLifecycleEventRequest) (*emptypb.Empty, error) {
	log.Printf("lifecycle event: project=%s event=%v", req.GetProjectId(), req.GetBuildEvent().GetEvent().GetEvent())
	return &emptypb.Empty{}, nil
}

// PublishBuildToolEventStream receives the actual Bazel build event stream
// for one invocation, persists each event via s.store, and extracts summary
// fields via index.EventParser for s.idx.
//
// The ack-ordering safety check below is adapted from BuildBuddy's
// PublishBuildToolEventStream (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/build_event_protocol/build_event_server/build_event_server.go
//
// Bazel numbers events in a stream 1, 2, 3, ... and may retry a whole stream
// from scratch on failure. We only ack once we've seen every sequence number
// contiguously from 1 — an incomplete or out-of-order stream fails the RPC so
// the client is forced to resend everything, instead of us storing a partial
// or duplicated invocation.
func (s *BuildEventServer) PublishBuildToolEventStream(stream besv1.PublishBuildEvent_PublishBuildToolEventStreamServer) error {
	ctx := stream.Context()
	var streamID *besv1.StreamId
	var acks []int64
	var parser *index.EventParser

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return s.finish(ctx, streamID, acks, parser, stream)
		}
		if err != nil {
			return err
		}

		ordered := req.GetOrderedBuildEvent()
		if streamID == nil {
			streamID = ordered.GetStreamId()
			if streamID.GetInvocationId() == "" {
				return status.Error(codes.FailedPrecondition, "missing invocation ID")
			}
			parser = index.NewEventParser(&index.Record{
				InvocationID: streamID.GetInvocationId(),
				CreatedAt:    time.Now(),
			})
		}

		if any := ordered.GetEvent().GetBazelEvent(); any != nil {
			bepEvent := &buildeventstream.BuildEvent{}
			if err := any.UnmarshalTo(bepEvent); err != nil {
				log.Printf("failed to unmarshal bazel_event: %v", err)
			} else {
				if err := s.store.AppendEvent(ctx, streamID.GetInvocationId(), bepEvent); err != nil {
					log.Printf("failed to store event: %v", err)
				}
				parser.ParseEvent(bepEvent)
			}
		}

		acks = append(acks, ordered.GetSequenceNumber())
	}
}

func (s *BuildEventServer) finish(ctx context.Context, streamID *besv1.StreamId, acks []int64, parser *index.EventParser, stream besv1.PublishBuildEvent_PublishBuildToolEventStreamServer) error {
	if streamID == nil {
		return nil
	}

	sort.Slice(acks, func(i, j int) bool { return acks[i] < acks[j] })
	for i, ack := range acks {
		want := int64(i + 1)
		if ack != want {
			return status.Errorf(codes.DataLoss, "event sequence mismatch for invocation %q: got %d, wanted %d", streamID.GetInvocationId(), ack, want)
		}
	}

	if err := s.store.Finalize(streamID.GetInvocationId()); err != nil {
		log.Printf("failed to finalize invocation %q: %v", streamID.GetInvocationId(), err)
	}

	if parser != nil {
		rec := parser.Record()
		rec.UpdatedAt = time.Now()
		if err := s.idx.Upsert(ctx, rec); err != nil {
			log.Printf("failed to index invocation %q: %v", streamID.GetInvocationId(), err)
		}
	}

	for _, ack := range acks {
		if err := stream.Send(&besv1.PublishBuildToolEventStreamResponse{
			StreamId:       streamID,
			SequenceNumber: ack,
		}); err != nil {
			return err
		}
	}
	return nil
}
