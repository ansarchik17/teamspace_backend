package graph

import "context"

type Node func(ctx context.Context) error

type Executor struct {
	nodes []Node
}

func NewExecutor(nodes ...Node) *Executor {
	return &Executor{nodes: nodes}
}

func (e *Executor) Execute(ctx context.Context) error {
	for _, node := range e.nodes {
		if err := node(ctx); err != nil {
			return err
		}
	}
	return nil
}
