package worker

import "fmt"

type Fake struct{}

func (Fake) Handle(in In) (*Out, error) {
	switch in.T {
	case "turn.start":
		return &Out{
			T:    "tool.request",
			ID:   "1",
			Name: "fs.read",
			Args: map[string]any{"path": "README.md"},
		}, nil
	case "tool.result":
		return &Out{T: "turn.finish"}, nil
	case "tool.denied":
		return &Out{T: "turn.finish"}, nil
	default:
		return nil, fmt.Errorf("unknown t: %s", in.T)
	}
}

func (Fake) Done(string) {}