package events

import (
	"testing"
	"time"
)

func TestPublishReachesEverySubscriberAndStampsDefaults(t *testing.T) {
	var b Bus
	a, stopA := b.Subscribe()
	c, stopC := b.Subscribe()
	defer stopA()
	defer stopC()
	b.Publish(Event{Type: "box.connected", Box: "devl"})
	for _, ch := range []<-chan Event{a, c} {
		e := <-ch
		if e.Type != "box.connected" || e.Origin != "calport" || e.Time.IsZero() {
			t.Fatalf("event = %+v", e)
		}
	}
	b.Publish(Event{Type: "worktree.created", Origin: "orca"})
	if e := <-a; e.Origin != "orca" {
		t.Fatalf("an explicit origin was overwritten: %+v", e)
	}
}

func TestASlowSubscriberDoesNotBlockPublishing(t *testing.T) {
	var b Bus
	_, stop := b.Subscribe()
	defer stop()
	done := make(chan struct{})
	go func() {
		for range 1000 {
			b.Publish(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a subscriber that never reads")
	}
}

func TestUnsubscribedChannelsReceiveNothing(t *testing.T) {
	var b Bus
	ch, stop := b.Subscribe()
	stop()
	b.Publish(Event{Type: "x"})
	select {
	case e := <-ch:
		t.Fatalf("received %+v after unsubscribing", e)
	default:
	}
}
