package agent

import (
	"context"

	graphflow "github.com/west6ide/workspace-backend/internal/agent/graph"
)

type graphExecutor struct {
	build func(state *FlowState) *graphflow.Executor
}

func NewGraphExecutor(build func(state *FlowState) *graphflow.Executor) FlowExecutor {
	return &graphExecutor{build: build}
}

func (e *graphExecutor) Execute(ctx context.Context, state *FlowState) error {
	return e.build(state).Execute(ctx)
}
