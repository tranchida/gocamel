package gocamel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sync/atomic"
	"time"
)

var globalExchangeID atomic.Uint64

// Common property constants
const (
	// File properties
	CamelFileName         = "CamelFileName"         // File name
	CamelFilePath         = "CamelFilePath"         // Full file path
	CamelFileLength       = "CamelFileLength"       // File size
	CamelFileLastModified = "CamelFileLastModified" // Last modification date
	CamelFileHost         = "CamelFileHost"         // Filesystem host

	// HTTP properties
	CamelHttpMethod       = "CamelHttpMethod"       // HTTP method
	CamelHttpUrl          = "CamelHttpUrl"          // HTTP URL
	CamelHttpPath         = "CamelHttpPath"         // HTTP path
	CamelHttpQuery        = "CamelHttpQuery"        // Query parameters
	CamelHttpResponseCode = "CamelHttpResponseCode" // HTTP response code
	CamelHttpResponseText = "CamelHttpResponseText" // HTTP response text

	// Message properties
	CamelMessageId         = "CamelMessageId"         // Unique message ID
	CamelMessageTimestamp  = "CamelMessageTimestamp"  // Message timestamp
	CamelMessageExchangeId = "CamelMessageExchangeId" // Exchange ID

	// Route properties
	CamelRouteId          = "CamelRouteId"          // Route ID
	CamelRouteGroup       = "CamelRouteGroup"       // Route group
	CamelRouteDescription = "CamelRouteDescription" // Route description

	// Error properties
	CamelExceptionCaught   = "CamelExceptionCaught"   // Caught exception
	CamelFailureEndpoint   = "CamelFailureEndpoint"   // Failing endpoint
	CamelFailureRouteId    = "CamelFailureRouteId"    // ID of the failing route
	CamelRedeliveryCounter = "CamelRedeliveryCounter" // Redelivery counter

	// Timer properties
	CamelTimerName      = "CamelTimerName"      // Timer name
	CamelTimerFiredTime = "CamelTimerFiredTime" // Timer fire time
	CamelTimerPeriod    = "CamelTimerPeriod"    // Timer period
	CamelTimerCounter   = "CamelTimerCounter"   // Timer counter

	// Context properties
	CamelContextName    = "CamelContextName"    // Context name
	CamelContextId      = "CamelContextId"      // Context ID
	CamelContextVersion = "CamelContextVersion" // Context version

	// EIP (Enterprise Integration Patterns) properties
	CamelSplitIndex            = "CamelSplitIndex"            // Index of the current part in a Split
	CamelSplitSize             = "CamelSplitSize"             // Total number of parts in a Split
	CamelSplitComplete         = "CamelSplitComplete"         // Boolean indicating whether this is the last part
	CamelAggregatorSize        = "CamelAggregatorSize"        // Number of aggregated messages
	CamelMulticastIndex        = "CamelMulticastIndex"        // Index of the current branch in a Multicast
	CamelMulticastSize         = "CamelMulticastSize"         // Total number of branches in a Multicast
	CamelMulticastComplete     = "CamelMulticastComplete"     // Boolean indicating whether this is the last branch
	CamelRecipientListIndex    = "CamelRecipientListIndex"    // Index of the current recipient in a RecipientList
	CamelRecipientListSize     = "CamelRecipientListSize"     // Total number of recipients in a RecipientList
	CamelRecipientListComplete = "CamelRecipientListComplete" // Boolean indicating whether this is the last recipient
	CamelRoutingSlipIndex      = "CamelRoutingSlipIndex"      // Index of the current step in a Routing Slip
	CamelRoutingSlipSize       = "CamelRoutingSlipSize"       // Total number of steps in a Routing Slip
	CamelRoutingSlipComplete   = "CamelRoutingSlipComplete"   // Boolean indicating whether this is the last routing step

	// Transaction properties
	CamelTransactionId     = "CamelTransactionId"     // Transaction ID
	CamelTransactionKey    = "CamelTransactionKey"    // Transaction key
	CamelTransactionStatus = "CamelTransactionStatus" // Transaction status

	// Security properties
	CamelAuthenticationScheme = "CamelAuthenticationScheme" // Authentication scheme
	CamelAuthenticationType   = "CamelAuthenticationType"   // Authentication type
	CamelAuthorizationPolicy  = "CamelAuthorizationPolicy"  // Authorization policy

	// Net properties
	CamelNetRemoteAddress = "CamelNetRemoteAddress" // Remote peer address (host:port)
	CamelNetLocalAddress  = "CamelNetLocalAddress"  // Local socket address (host:port)
)

// Synchronization defines the interface for callbacks triggered when an Exchange is done.
type Synchronization interface {
	// OnComplete is called when the Exchange was processed successfully.
	OnComplete(exchange *Exchange)
	// OnFailure is called when the Exchange failed during processing.
	OnFailure(exchange *Exchange)
}

// Exchange represents the exchange context of a message in a route.
//
// Thread-safety: Exchange is NOT safe for concurrent use. The In/Out Message
// objects and the Properties map have no internal synchronization. Components
// and EIPs that fan out to multiple goroutines (Multicast, Splitter,
// RecipientList) MUST call DeepCopy() once per branch in the parent goroutine
// before launching workers. The parent Exchange is read-only for the duration
// of the fan-out and only becomes writable again after wg.Wait() returns and
// the aggregation strategy publishes its result back.
type Exchange struct {
	ID               string
	Context          context.Context
	In               *Message
	Out              *Message
	Properties       map[string]any
	Created          time.Time
	Modified         time.Time
	Error            error
	synchronizations []Synchronization
}

func NewExchange(ctx context.Context) *Exchange {
	now := time.Now()
	return &Exchange{
		ID:               fmt.Sprintf("ex-%d", globalExchangeID.Add(1)),
		Context:          ctx,
		In:               NewMessage(),
		Out:              NewMessage(),
		Properties:       make(map[string]any),
		Created:          now,
		Modified:         now,
		synchronizations: make([]Synchronization, 0),
	}
}

// AddSynchronization adds a synchronization callback to the exchange.
func (e *Exchange) AddSynchronization(s Synchronization) {
	e.synchronizations = append(e.synchronizations, s)
}

// Done signals that the exchange processing is complete.
// It triggers the OnComplete or OnFailure callbacks of registered synchronizations.
func (e *Exchange) Done(err error) {
	e.Error = err
	for _, s := range e.synchronizations {
		if err != nil {
			s.OnFailure(e)
		} else {
			s.OnComplete(e)
		}
	}
}

// CompleteExchange finalizes a consumer-created exchange after route processing.
// It normalizes the ErrStopRouting control signal to success (a stopped route is
// not a failure), fires the exchange synchronizations via Done, and returns the
// error the consumer should report (nil when routing was merely stopped).
// Every consumer that creates exchanges must call this so that Synchronization
// callbacks (AddSynchronization) fire consistently across components.
func CompleteExchange(exchange *Exchange, err error) error {
	if errors.Is(err, ErrStopRouting) {
		err = nil
	}
	exchange.Done(err)
	return err
}

func (e *Exchange) GetIn() *Message {
	return e.In
}

func (e *Exchange) GetOut() *Message {
	return e.Out
}

// HasOut returns true if the Out message has been explicitly set (non-nil body).
func (e *Exchange) HasOut() bool {
	return e.Out.GetBody() != nil
}

// GetResponse returns In when no output is pending, otherwise a message copy
// combining In with the pending Out body and header overrides.
func (e *Exchange) GetResponse() *Message {
	if !e.HasOut() && len(e.Out.Headers) == 0 {
		return e.In
	}
	response := e.In.Copy()
	if e.HasOut() {
		response.SetBody(e.Out.GetBody())
	}
	response.SetHeaders(e.Out.GetHeaders())
	return response
}

func (e *Exchange) SetBody(body any) {
	e.In.SetBody(body)
	e.Modified = time.Now()
}

func (e *Exchange) GetBody() any {
	return e.In.GetBody()
}

func (e *Exchange) GetBodyAsString() (string, bool) {
	return e.In.GetBodyAsString()
}

func (e *Exchange) GetBodyAsInt() (int, bool) {
	return e.In.GetBodyAsInt()
}

func (e *Exchange) GetBodyAsBool() (bool, bool) {
	return e.In.GetBodyAsBool()
}

func (e *Exchange) SetHeader(key string, value any) {
	e.In.SetHeader(key, value)
	e.Modified = time.Now()
}

func (e *Exchange) GetHeader(key string) (any, bool) {
	return e.In.GetHeader(key)
}

func (e *Exchange) GetHeaderAsString(key string) (string, bool) {
	return e.In.GetHeaderAsString(key)
}

func (e *Exchange) GetHeaderAsInt(key string) (int, bool) {
	return e.In.GetHeaderAsInt(key)
}

func (e *Exchange) GetHeaderAsBool(key string) (bool, bool) {
	return e.In.GetHeaderAsBool(key)
}

func (e *Exchange) SetProperty(key string, value any) {
	e.Properties[key] = value
	e.Modified = time.Now()
}

func (e *Exchange) GetProperty(key string) (any, bool) {
	value, exists := e.Properties[key]
	return value, exists
}

func (e *Exchange) GetPropertyOrDefault(key string, defaultValue any) any {
	if value, exists := e.Properties[key]; exists {
		return value
	}
	return defaultValue
}

func (e *Exchange) RemoveProperty(key string) {
	delete(e.Properties, key)
	e.Modified = time.Now()
}

// RemoveProperties removes properties matching the given pattern,
// except those matching any of the exclusion patterns.
// The pattern supports Apache Camel wildcards '*' (e.g. 'Camel*').
func (e *Exchange) RemoveProperties(pattern string, excludePatterns ...string) {
	mainRegex := patternToRegex(pattern)

	var excludeRegexes []*regexp.Regexp
	for _, p := range excludePatterns {
		excludeRegexes = append(excludeRegexes, patternToRegex(p))
	}

	for key := range e.Properties {
		if mainRegex.MatchString(key) {
			shouldExclude := false
			for _, exRegex := range excludeRegexes {
				if exRegex.MatchString(key) {
					shouldExclude = true
					break
				}
			}

			if !shouldExclude {
				delete(e.Properties, key)
				e.Modified = time.Now()
			}
		}
	}
}

func (e *Exchange) HasProperty(key string) bool {
	_, exists := e.Properties[key]
	return exists
}

func (e *Exchange) GetProperties() map[string]any {
	out := make(map[string]any, len(e.Properties))
	maps.Copy(out, e.Properties)
	return out
}

func (e *Exchange) SetProperties(properties map[string]any) {
	for key, value := range properties {
		e.Properties[key] = value
	}
	e.Modified = time.Now()
}

func (e *Exchange) ClearProperties() {
	e.Properties = make(map[string]any)
	e.Modified = time.Now()
}

func (e *Exchange) GetPropertyAsString(key string) (string, bool) {
	if value, exists := e.Properties[key]; exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

func (e *Exchange) GetPropertyAsInt(key string) (int, bool) {
	if value, exists := e.Properties[key]; exists {
		if i, ok := value.(int); ok {
			return i, true
		}
		// Handle float64 when unmarshaled from JSON
		if f, ok := value.(float64); ok {
			return int(f), true
		}
	}
	return 0, false
}

func (e *Exchange) GetPropertyAsBool(key string) (bool, bool) {
	if value, exists := e.Properties[key]; exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

func (e *Exchange) GetPropertyAsFloat(key string) (float64, bool) {
	if value, exists := e.Properties[key]; exists {
		if f, ok := value.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func (e *Exchange) GetPropertyAsTime(key string) (time.Time, bool) {
	if value, exists := e.Properties[key]; exists {
		if t, ok := value.(time.Time); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func (e *Exchange) GetPropertyAsDuration(key string) (time.Duration, bool) {
	if value, exists := e.Properties[key]; exists {
		if d, ok := value.(time.Duration); ok {
			return d, true
		}
	}
	return 0, false
}

func (e *Exchange) GetPropertyAsMap(key string) (map[string]any, bool) {
	if value, exists := e.Properties[key]; exists {
		if m, ok := value.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}

func (e *Exchange) GetPropertyAsSlice(key string) ([]any, bool) {
	if value, exists := e.Properties[key]; exists {
		if s, ok := value.([]any); ok {
			return s, true
		}
	}
	return nil, false
}

// PropagateOutToIn copies the Out body and headers into In when a processor has written
// a response to Out. This is the standard inter-step propagation in a Camel pipeline.
func (e *Exchange) PropagateOutToIn() {
	if body := e.Out.GetBody(); body != nil {
		e.In.SetBody(body)
	}
	for k, v := range e.Out.GetHeaders() {
		e.In.SetHeader(k, v)
	}
}

// branchScopedProperties are the properties an EIP sets on each of its branch
// exchanges to describe that branch's position in the fan-out. They describe
// the branch, not the parent, so they must not survive when the aggregated
// result is published back onto the parent exchange — a downstream processor
// seeing CamelSplitComplete=true from a finished Split (or a nested Split
// leaking into its enclosing one) would draw the wrong conclusion.
var branchScopedProperties = []string{
	CamelSplitIndex, CamelSplitSize, CamelSplitComplete,
	CamelMulticastIndex, CamelMulticastSize, CamelMulticastComplete,
	CamelRecipientListIndex, CamelRecipientListSize, CamelRecipientListComplete,
	CamelRoutingSlipIndex, CamelRoutingSlipSize, CamelRoutingSlipComplete,
}

// publishAggregatedResult copies the aggregated In/Out messages and properties
// from a fan-out branch back onto the parent exchange, dropping the
// branch-scoped bookkeeping properties listed in branchScopedProperties.
//
// Properties the parent already carried but the branch did not are preserved:
// the branch was derived from the parent by Copy/DeepCopy, so anything missing
// from the branch was deliberately removed by the route, except for the scoped
// keys stripped here.
func publishAggregatedResult(parent, aggregated *Exchange) {
	if aggregated == nil {
		return
	}
	parent.In = aggregated.In
	parent.Out = aggregated.Out

	properties := make(map[string]any, len(aggregated.Properties))
	maps.Copy(properties, aggregated.Properties)
	for _, key := range branchScopedProperties {
		// Only drop a scoped key the parent did not itself define: an
		// enclosing Split legitimately owns its own CamelSplitIndex.
		if _, parentHad := parent.Properties[key]; !parentHad {
			delete(properties, key)
			continue
		}
		properties[key] = parent.Properties[key]
	}
	parent.Properties = properties
}

// Copy returns a shallow copy of the exchange. The In/Out Message bodies and
// header/property values are shared by reference (see Message.Copy); use
// DeepCopy when the copy crosses a goroutine boundary that runs concurrently
// with the original.
//
// The synchronizations slice is intentionally NOT copied: completion callbacks
// are bound to the original exchange's lifecycle and should not fire again
// when a derived copy finishes (a Multicast/Splitter branch is a different
// logical exchange). Add new Synchronization callbacks to the copy explicitly
// if its own completion must be observed.
func (e *Exchange) Copy() *Exchange {
	cloned := NewExchange(e.Context)
	cloned.In = e.In.Copy()
	cloned.Out = e.Out.Copy()
	maps.Copy(cloned.Properties, e.Properties)

	cloned.Created = e.Created
	cloned.Modified = time.Now()
	cloned.Error = e.Error

	return cloned
}

// DeepCopy is like Copy but additionally duplicates the common mutable
// container types held in bodies, headers, and properties (see
// Message.DeepCopy). Use it whenever the copy is handed to another goroutine
// that runs concurrently with the original exchange (WireTap, parallel
// Multicast branches).
func (e *Exchange) DeepCopy() *Exchange {
	cloned := NewExchange(e.Context)
	cloned.In = e.In.DeepCopy()
	cloned.Out = e.Out.DeepCopy()
	for k, v := range e.Properties {
		cloned.Properties[k] = deepCopyValue(v)
	}

	cloned.Created = e.Created
	cloned.Modified = time.Now()
	cloned.Error = e.Error

	return cloned
}
