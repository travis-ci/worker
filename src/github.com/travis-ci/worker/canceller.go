package worker

// A Canceller allows you to subscribe to and unsubscribe from cancellation
// messages for a given job ID.
type Canceller interface {
	// Subscribe will set up a subscription for cancellation messages for the
	// given job ID. When a cancellation message comes in, the channel will be
	// closed. Only one subscription per job ID is valid, if there's already a
	// subscription set up, an error will be returned.
	Subscribe(id uint64, ch chan<- struct{}) error

	// Unsubscribe removes the existing subscription for the given job ID.
	Unsubscribe(id uint64)
}
