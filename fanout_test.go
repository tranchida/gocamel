package gocamel

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type branchCompletion struct{ succeeded, failed atomic.Int32 }

func (s *branchCompletion) OnComplete(*Exchange) { s.succeeded.Add(1) }
func (s *branchCompletion) OnFailure(*Exchange)  { s.failed.Add(1) }

func TestFanOutCompletesBranchesAndContainsStrategyPanics(t *testing.T) {
	for _, kind := range []string{"split", "multicast", "recipients"} {
		for _, parallel := range []bool{false, true} {
			for _, outcome := range []string{"success", "failure", "panic"} {
				t.Run(fmt.Sprintf("%s/parallel=%t/%s", kind, parallel, outcome), func(t *testing.T) {
					c := NewCamelContext()
					branchSync, parentSync := &branchCompletion{}, &branchCompletion{}
					failure := errors.New("branch failed")
					processor := ProcessorFunc(func(e *Exchange) error {
						e.AddSynchronization(branchSync)
						if outcome == "failure" {
							return failure
						}
						return nil
					})
					var strategy AggregationStrategy
					if outcome == "panic" {
						strategy = reviewPanicStrategy{}
					}
					var fanout Processor
					switch kind {
					case "split":
						s := NewSplitter(func(*Exchange) (any, error) { return []string{"a", "b"}, nil }).SetParallelProcessing(parallel).SetAggregationStrategy(strategy)
						s.AddProcessor(processor)
						fanout = s
					case "multicast":
						m := NewMulticast().SetParallelProcessing(parallel).SetAggregationStrategy(strategy)
						m.AddProcessor(processor)
						m.AddProcessor(processor)
						fanout = m
					case "recipients":
						for _, uri := range []string{"direct:a", "direct:b"} {
							_, err := c.CreateRouteBuilder().From(uri).Process(processor).Build()
							require.NoError(t, err)
						}
						fanout = NewRecipientList(c).SetStaticURIs("direct:a,direct:b").SetParallelProcessing(parallel).SetAggregationStrategy(strategy)
					}
					require.NoError(t, c.Start())
					defer c.Stop()
					ex := NewExchange(context.Background())
					ex.AddSynchronization(parentSync)
					err := ProcessSafely(fanout, ex)
					if outcome == "success" {
						require.NoError(t, err)
						assert.Equal(t, int32(2), branchSync.succeeded.Load())
					} else {
						if outcome == "panic" {
							assert.ErrorIs(t, err, ErrProcessorPanic)
						} else {
							assert.ErrorIs(t, err, failure)
						}
						expected := int32(1)
						if parallel {
							expected = 2
						}
						assert.Equal(t, expected, branchSync.failed.Load())
					}
					assert.Zero(t, parentSync.succeeded.Load()+parentSync.failed.Load(), "branches must not complete their parent")
					_ = CompleteExchange(ex, err)
					assert.Equal(t, int32(1), parentSync.succeeded.Load()+parentSync.failed.Load())
				})
			}
		}
	}
}

type fanOutConcatFromInStrategy struct{}

func (fanOutConcatFromInStrategy) Aggregate(oldExchange, newExchange *Exchange) *Exchange {
	if oldExchange == nil {
		return newExchange
	}
	a, _ := oldExchange.GetIn().GetBodyAsString()
	b, _ := newExchange.GetIn().GetBodyAsString()
	oldExchange.GetIn().SetBody(a + "," + b)
	return oldExchange
}

func TestFanOut_PropagatesBranchOutToInBeforeStrategy(t *testing.T) {
	for _, kind := range []string{"split", "multicast", "recipients"} {
		for _, parallel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parallel=%t", kind, parallel), func(t *testing.T) {
				c := NewCamelContext()
				strategy := fanOutConcatFromInStrategy{}

				var fanout Processor
				switch kind {
				case "split":
					s := NewSplitter(func(*Exchange) (any, error) { return []string{"a", "b"}, nil }).
						SetParallelProcessing(parallel).
						SetAggregationStrategy(strategy)
					s.AddProcessor(ProcessorFunc(func(e *Exchange) error {
						body, _ := e.GetIn().GetBodyAsString()
						e.GetOut().SetBody("out-" + body)
						return nil
					}))
					fanout = s
				case "multicast":
					m := NewMulticast().
						SetParallelProcessing(parallel).
						SetAggregationStrategy(strategy)
					m.AddProcessor(ProcessorFunc(func(e *Exchange) error {
						e.GetOut().SetBody("out-a")
						return nil
					}))
					m.AddProcessor(ProcessorFunc(func(e *Exchange) error {
						e.GetOut().SetBody("out-b")
						return nil
					}))
					fanout = m
				case "recipients":
					for _, name := range []string{"a", "b"} {
						label := name
						_, err := c.CreateRouteBuilder().
							From("direct:fanout-" + label).
							SetBody("out-" + label).
							Build()
						require.NoError(t, err)
					}
					fanout = NewRecipientList(c).
						SetStaticURIs("direct:fanout-a,direct:fanout-b").
						SetParallelProcessing(parallel).
						SetAggregationStrategy(strategy)
				}

				require.NoError(t, c.Start())
				t.Cleanup(func() { _ = c.Stop() })

				ex := NewExchange(context.Background())
				ex.SetBody("initial")
				require.NoError(t, fanout.Process(ex))
				assert.Equal(t, "out-a,out-b", ex.GetIn().GetBody())
			})
		}
	}
}
