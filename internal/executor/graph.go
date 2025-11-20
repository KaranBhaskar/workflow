package executor

import (
	"errors"
	"strings"

	"workflow/internal/workflow"
)

type graph struct {
	nodes    map[string]workflow.Node
	outbound map[string][]workflow.Edge
	roots    []string
}

func buildGraph(definition workflow.Definition) (graph, error) {
	nodeByID := make(map[string]workflow.Node, len(definition.Nodes))
	inDegree := make(map[string]int, len(definition.Nodes))
	outbound := make(map[string][]workflow.Edge, len(definition.Nodes))
	for _, node := range definition.Nodes {
		nodeByID[node.ID] = node
		inDegree[node.ID] = 0
	}
	for _, edge := range definition.Edges {
		outbound[edge.From] = append(outbound[edge.From], edge)
		inDegree[edge.To]++
	}

	roots := make([]string, 0, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if inDegree[node.ID] == 0 {
			roots = append(roots, node.ID)
		}
	}

	if len(roots) == 0 {
		return graph{}, errors.New("workflow contains a cycle and cannot be executed")
	}

	return graph{
		nodes:    nodeByID,
		outbound: outbound,
		roots:    roots,
	}, nil
}

func (g graph) next(node workflow.Node, output any) []string {
	edges := g.outbound[node.ID]
	if node.Type != "condition" {
		next := make([]string, 0, len(edges))
		for _, edge := range edges {
			next = append(next, edge.To)
		}
		return next
	}

	matched := false
	if payload, ok := output.(map[string]any); ok {
		if value, ok := payload["matched"].(bool); ok {
			matched = value
		}
	}

	label := "false"
	if matched {
		label = "true"
	}

	next := make([]string, 0, 1)
	for _, edge := range edges {
		if strings.EqualFold(strings.TrimSpace(edge.Condition), label) {
			next = append(next, edge.To)
		}
	}

	return next
}

func buildRunOutput(lastNodeID string, stepOutputs map[string]any) any {
	if lastNodeID == "" {
		return nil
	}

	return map[string]any{
		"last_node": lastNodeID,
		"result":    stepOutputs[lastNodeID],
	}
}
