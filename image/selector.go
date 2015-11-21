package image

// Selector is the interface for selecting an image!
type Selector interface {
	Select(*Params) (string, error)
}
