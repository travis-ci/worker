package image

import gocontext "context"

// Selector is the interface for selecting an image!
type Selector interface {
	Select(gocontext.Context, *Params) (string, error)
}
