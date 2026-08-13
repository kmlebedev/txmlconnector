package tcServer

import (
	"errors"
	"testing"
)

func TestBrokerBroadcastsToEverySubscriber(t *testing.T) {
	b := newBroker()
	first := b.subscribe(1)
	second := b.subscribe(1)
	defer first.close()
	defer second.close()

	b.publish("message")
	for name, sub := range map[string]*subscription{"first": first, "second": second} {
		select {
		case got := <-sub.messages:
			if got != "message" {
				t.Fatalf("%s subscriber received %q", name, got)
			}
		default:
			t.Fatalf("%s subscriber received no message", name)
		}
	}
}

func TestBrokerDetachesSlowSubscriber(t *testing.T) {
	b := newBroker()
	sub := b.subscribe(1)
	b.publish("first")
	b.publish("second")

	select {
	case <-sub.done:
	default:
		t.Fatal("slow subscriber was not detached")
	}
	if !errors.Is(sub.error(), errSlowSubscriber) {
		t.Fatalf("error = %v", sub.error())
	}
}
