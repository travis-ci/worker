package worker

type writeFolder interface {
	WriteFold(string, []byte) (int, error)
}
