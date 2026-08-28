package worker

type Worker interface {
	Handle(in In, emit func(Out) error) (*Out, error)
	Done(runID string)
}