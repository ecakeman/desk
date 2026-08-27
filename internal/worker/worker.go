package worker

type Worker interface{
	Handle(in In)(*Out,error)
	Done(runID string)
}