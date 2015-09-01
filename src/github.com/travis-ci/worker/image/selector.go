package image

type Selector interface {
	Select(*Params) (string, error)
}
