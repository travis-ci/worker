package worker

import "testing"

func TestCancellationBroadcaster(t *testing.T) {
	cb := NewCancellationBroadcaster()

	ch1_1 := cb.Subscribe(1)
	ch1_2 := cb.Subscribe(1)
	ch1_3 := cb.Subscribe(1)
	ch2 := cb.Subscribe(2)

	cb.Unsubscribe(1, ch1_2)

	cb.Broadcast(1, CancellationCommand{})
	cb.Broadcast(1, CancellationCommand{})

	assertClosed(t, "ch1_1", ch1_1)
	assertWaiting(t, "ch1_2", ch1_2)
	assertClosed(t, "ch1_3", ch1_3)
	assertWaiting(t, "ch2", ch2)
}

func assertClosed(t *testing.T, name string, ch <-chan CancellationCommand) {
	select {
	case _, ok := (<-ch):
		if ok {
			t.Errorf("expected %s to be closed, but it received a value", name)
		}
	default:
		t.Errorf("expected %s to be closed, but it wasn't", name)
	}
}

func assertWaiting(t *testing.T, name string, ch <-chan CancellationCommand) {
	select {
	case _, ok := (<-ch):
		if ok {
			t.Errorf("expected %s to be not be closed and not have a value, but it received a value", name)
		} else {
			t.Errorf("expected %s to be not be closed and not have a value, but it was closed", name)
		}
	default:
	}
}
