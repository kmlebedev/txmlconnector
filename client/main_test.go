package tcClient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
	"google.golang.org/grpc/metadata"
)

type responseStream struct {
	ctx         context.Context
	messages    []string
	mu          sync.Mutex
	next        int
	allReceived chan struct{}
	received    sync.Once
}

func (s *responseStream) Header() (metadata.MD, error) { return nil, nil }
func (s *responseStream) Trailer() metadata.MD         { return nil }
func (s *responseStream) CloseSend() error             { return nil }
func (s *responseStream) Context() context.Context     { return s.ctx }
func (s *responseStream) SendMsg(interface{}) error    { return nil }
func (s *responseStream) RecvMsg(interface{}) error    { return nil }

func (s *responseStream) Recv() (*pb.DataResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == len(s.messages) {
		return nil, io.EOF
	}
	message := s.messages[s.next]
	s.next++
	if s.next == len(s.messages) {
		s.received.Do(func() { close(s.allReceived) })
	}
	return &pb.DataResponse{Message: message}, nil
}

func TestHandleMessageRoutesQuotes(t *testing.T) {
	tc := TCClient{QuotesChan: make(chan commands.Quotes, 1)}
	if err := tc.handleMessage(`<quotes><quote secid="42"><price>123.45</price></quote></quotes>`); err != nil {
		t.Fatal(err)
	}
	quotes := <-tc.QuotesChan
	if len(quotes.Items) != 1 || quotes.Items[0].SecId != 42 || quotes.Items[0].Price != 123.45 {
		t.Fatalf("quotes = %+v", quotes)
	}
	if quotes.Time.IsZero() {
		t.Fatal("quotes timestamp was not set")
	}
}

func TestHandleMessageStoresPositionsAndNotifies(t *testing.T) {
	tc := TCClient{ResponseChannel: make(chan string, 1)}
	if err := tc.handleMessage(`<positions></positions>`); err != nil {
		t.Fatal(err)
	}
	if tc.Data.Positions == nil {
		t.Fatal("positions were not stored")
	}
	if got := <-tc.ResponseChannel; got != "positions" {
		t.Fatalf("notification = %q", got)
	}
}

func TestHandleMessageRejectsMalformedAndUnknownXML(t *testing.T) {
	tc := TCClient{}
	if err := tc.handleMessage(`<quotes>`); err == nil {
		t.Fatal("malformed XML was accepted")
	}
	if err := tc.handleMessage(`<new_message/>`); !errors.Is(err, errUnknownMessage) {
		t.Fatalf("unknown message error = %v", err)
	}
}

func TestLoopReadingFromStreamKeepsReceivingWhileDispatchIsBlocked(t *testing.T) {
	const messageCount = 256
	messages := make([]string, messageCount)
	for i := range messages {
		messages[i] = fmt.Sprintf(`<sec_info_upd><secid>%d</secid></sec_info_upd>`, i)
	}
	stream := &responseStream{
		ctx:         context.Background(),
		messages:    messages,
		allReceived: make(chan struct{}),
	}
	tc := TCClient{
		SecInfoUpdChan:  make(chan commands.SecInfoUpd, 1),
		ShutdownChannel: make(chan bool, 1),
		ctx:             context.Background(),
	}
	done := make(chan struct{})
	go func() {
		tc.loopReadingFromStream(stream)
		close(done)
	}()

	receivedWithoutDraining := true
	select {
	case <-stream.allReceived:
	case <-time.After(time.Second):
		receivedWithoutDraining = false
	}

	for i := range messageCount {
		select {
		case event := <-tc.SecInfoUpdChan:
			if event.SecId != i {
				t.Fatalf("event %d has secid %d", i, event.SecId)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out draining dispatched messages")
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream reader did not stop after EOF")
	}
	if !receivedWithoutDraining {
		t.Fatal("gRPC receive loop was blocked by the typed event consumer")
	}
}

func TestLoopReadingFromStreamCancellationUnblocksDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &responseStream{
		ctx:         ctx,
		messages:    []string{`<sec_info_upd><secid>1</secid></sec_info_upd>`},
		allReceived: make(chan struct{}),
	}
	tc := TCClient{
		SecInfoUpdChan:  make(chan commands.SecInfoUpd),
		ShutdownChannel: make(chan bool, 1),
		ctx:             ctx,
		cancel:          cancel,
	}
	done := make(chan struct{})
	go func() {
		tc.loopReadingFromStream(stream)
		close(done)
	}()

	select {
	case <-stream.allReceived:
	case <-time.After(time.Second):
		t.Fatal("stream message was not received")
	}
	tc.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock typed event dispatch")
	}
	select {
	case <-tc.ShutdownChannel:
	default:
		t.Fatal("client shutdown was not signaled")
	}
}
