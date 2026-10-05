package gocamel

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A When expression that fails to parse at construction must not be
// re-parsed by Process: concurrent consumers (HTTP, SEDA workers) would
// otherwise write when.template without synchronization. The parse failure
// is made transient by tightening MaxSimpleInputBytes during When(), so a
// re-parse in Process would succeed and race on the write.
func TestChoiceProcessor_ConcurrentProcessDoesNotReparse(t *testing.T) {
	expression := "${header.kind} == '" + strings.Repeat("a", 64) + "'"

	saved := MaxSimpleInputBytes
	MaxSimpleInputBytes = 16
	choice := NewChoiceProcessor().When(expression, ProcessorFunc(func(*Exchange) error { return nil }))
	MaxSimpleInputBytes = saved

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = choice.Process(NewExchange(context.Background()))
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MaxSimpleInputBytes", "the construction-time parse error must surface")
	}
	assert.ErrorContains(t, choice.Validate(), "MaxSimpleInputBytes")
}

func TestChoiceProcessor_InvalidWhenSurfacesStoredError(t *testing.T) {
	oversized := strings.Repeat("a", MaxSimpleInputBytes+1)
	choice := NewChoiceProcessor().When(oversized, ProcessorFunc(func(*Exchange) error { return nil }))

	err := choice.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse when expression")
	assert.Error(t, choice.Validate())
}
