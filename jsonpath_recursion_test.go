package gocamel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONPathRecursiveDescent_CyclicMapTerminates(t *testing.T) {
	m := map[string]any{"id": 1}
	m["self"] = m

	// Without cycle detection this recursed until the goroutine stack was
	// exhausted, which is a fatal error that cannot be recovered.
	res, err := EvaluateJSONPath("$..id", m)
	require.NoError(t, err)
	assert.Equal(t, 1, res)
}

func TestJSONPathRecursiveDescent_CyclicSliceTerminates(t *testing.T) {
	s := make([]any, 2)
	s[0] = map[string]any{"id": 7}
	s[1] = s
	doc := map[string]any{"items": s}

	// The slice contains itself through a map; the back-edge must be skipped.
	res, err := EvaluateJSONPath("$..id", doc)
	require.NoError(t, err)
	assert.Equal(t, 7, res)
}

func TestJSONPathRecursiveDescent_DepthLimitReturnsError(t *testing.T) {
	// Build a non-cyclic chain of maps deeper than maxJSONPathDepth.
	root := map[string]any{"id": "leaf"}
	cur := root
	for i := 0; i < maxJSONPathDepth+50; i++ {
		next := map[string]any{}
		cur["child"] = next
		cur = next
	}

	res, err := EvaluateJSONPath("$..id", root)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "recursive descent exceeded maximum depth")
}

func TestJSONPathRecursiveDescent_NestedDocumentReturnsAllMatches(t *testing.T) {
	doc := map[string]any{
		"id": 1,
		"children": []any{
			map[string]any{"id": 2, "children": []any{
				map[string]any{"id": 3},
			}},
			map[string]any{"id": 4},
		},
	}

	res, err := EvaluateJSONPath("$..id", doc)
	require.NoError(t, err)
	values, ok := res.([]any)
	require.True(t, ok, "expected multiple matches, got %T", res)
	assert.ElementsMatch(t, []any{1, 2, 3, 4}, values)
}

func TestJSONPathRecursiveDescent_SharedNonCyclicContainerIsVisitedPerBranch(t *testing.T) {
	shared := map[string]any{"id": "x"}
	doc := map[string]any{
		"left":  shared,
		"right": shared,
	}

	res, err := EvaluateJSONPath("$..id", doc)
	require.NoError(t, err)
	values, ok := res.([]any)
	require.True(t, ok, "expected two matches, got %T", res)
	assert.Equal(t, []any{"x", "x"}, values)
}
