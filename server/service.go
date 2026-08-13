package tcServer

import (
	"context"
	"errors"

	"github.com/kmlebedev/txmlconnector/connector"
	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const defaultSubscriberBuffer = 256

type service struct {
	pb.UnimplementedConnectServiceServer
	connector        connector.Connector
	messages         *broker
	subscriberBuffer int
}

func newService(native connector.Connector, subscriberBuffer int) *service {
	if subscriberBuffer < 1 {
		subscriberBuffer = defaultSubscriberBuffer
	}
	return &service{
		connector:        native,
		messages:         newBroker(),
		subscriberBuffer: subscriberBuffer,
	}
}

func (s *service) handleMessage(message string) {
	s.messages.publish(message)
}

func (s *service) SendCommand(ctx context.Context, request *pb.SendCommandRequest) (*pb.SendCommandResponse, error) {
	if request == nil || request.Message == "" {
		return nil, status.Error(codes.InvalidArgument, "command message is required")
	}
	message, err := s.connector.SendCommand(ctx, request.Message)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Errorf(codes.Internal, "send TRANSAQ command: %v", err)
	}
	return &pb.SendCommandResponse{Message: message}, nil
}

func (s *service) FetchResponseData(_ *pb.DataRequest, stream pb.ConnectService_FetchResponseDataServer) error {
	sub := s.messages.subscribe(s.subscriberBuffer)
	defer sub.close()
	// Sending headers is a readiness handshake. Clients can wait for Header before
	// issuing connect and avoid losing the initial snapshot produced by the DLL.
	if err := stream.SendHeader(metadata.MD{}); err != nil {
		return err
	}

	for {
		select {
		case message := <-sub.messages:
			if err := stream.Send(&pb.DataResponse{Message: message}); err != nil {
				return err
			}
		case <-sub.done:
			if errors.Is(sub.error(), errSlowSubscriber) {
				return status.Error(codes.ResourceExhausted, errSlowSubscriber.Error())
			}
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}
