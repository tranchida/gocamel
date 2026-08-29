package gocamel

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// The Simple Language implementation is split across this package:
//   simple_language.go              — types, regex globals, parser, templates
//   simple_language_eval.go         — evaluateVariable + dispatch helpers
//   simple_language_access.go       — bracket / dot / null-safe property access
//   simple_language_strings.go      — applyStringFunction (chained string verbs)
//   simple_language_compare.go      — toBool, checkType, containsValue, compareValues
//   simple_language_math.go         — toFloat64 + math.* numeric functions
//   simple_language_functions.go    — file:, date:, base64: function verbs
//   simple_language_processors.go   — SimpleSetBodyProcessor and friends

// SimpleLanguage represents the Simple Language expression evaluator
type SimpleLanguage struct{}

// Expression defines the interface for evaluating expressions
type Expression interface {
	Evaluate(exchange *Exchange) (interface{}, error)
	EvaluateAsString(exchange *Exchange) (string, error)
}

// ExpressionFunc is a function type that implements Expression
type ExpressionFunc func(*Exchange) (interface{}, error)

// Evaluate implements the Expression interface
func (f ExpressionFunc) Evaluate(exchange *Exchange) (interface{}, error) {
	return f(exchange)
}

// EvaluateAsString implements the Expression interface
func (f ExpressionFunc) EvaluateAsString(exchange *Exchange) (string, error) {
	result, err := f(exchange)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result), nil
}

// SimpleTemplate is a compiled simple language expression template
type SimpleTemplate struct {
	expression string
	isLiteral  bool
	parts      []templatePart
}

type templatePart struct {
	isVariable bool
	content    string
}

// Compiled Simple Language regex patterns.
//
// All patterns are MustCompile'd at package init time so a malformed pattern
// fails loudly at startup rather than at the first matching message.
// Because Go's init runs whenever the package is loaded, simply running
// `go test ./...` (or `go vet`/`go build`) is enough to detect a malformed
// pattern introduced by a PR — no dedicated regex test is needed.
var (
	// Matches ${expression}
	placeholderRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

	// Matches body, header.name, exchangeProperty.name
	// Now supports names with hyphens and other characters
	dotNotationRegex = regexp.MustCompile(`^(body)$|^(header|[a-zA-Z]+)\.([a-zA-Z0-9_\-\.]+)$`)

	// Matches date:command:FORMAT (command can be now, header.xxx, property.xxx, file)
	// Supports offsets like +24h, -10m
	dateFunctionRegex = regexp.MustCompile(`^date:([^+\-:]+)([\+\-][0-9]+[a-z]+)?(?::(.+))?$`)

	// Matches random(MAX)
	randomFunctionRegex = regexp.MustCompile(`^random\((\d+)\)$`)

	// Matches math(expr)
	mathFunctionRegex = regexp.MustCompile(`^math\((.+)\)$`)

	// Matches numeric functions: round(val), floor(val), etc.
	numericFunctionRegex = regexp.MustCompile(`^(round|floor|ceil|abs|sqrt|sin|cos|tan)\((.+)\)$`)

	// Matches file:functions
	fileFunctionRegex = regexp.MustCompile(`^file:([a-zA-Z0-9_\.]+)$`)

	// Matches base64:command:target
	base64FunctionRegex = regexp.MustCompile(`^base64:(encode|decode):(.+)$`)

	// Matches jsonpath:path or jsonpath:header.name:path
	jsonpathFunctionRegex = regexp.MustCompile(`^jsonpath:(.+)$`)

	// Matches uuid
	uuidFunctionRegex = regexp.MustCompile(`^uuid$`)

	// Comparison operators with proper order (multi-char operators first)
	comparisonRegex = regexp.MustCompile(`^(.+?)(==|!=|>=|<=|>|<)(.+)$`)

	// String operation operators
	stringOpRegex = regexp.MustCompile(`^(.+?)\s+(contains|startsWith|endsWith|regex)\s+(.+)$`)

	// Logical AND - split by && first (lower precedence than comparisons)
	// We handle || at higher level
	ternaryRegex = regexp.MustCompile(`^(.+?)\?\s*(.+?)\s*:\s*(.+)$`)

	// Null-safe dot notation: body?.field or header?.name
	nullSafeDotNotationRegex = regexp.MustCompile(`^(body|header|exchangeProperty)\?\.([a-zA-Z0-9_\-]+)(.*)$`)

	// Property access after body: body.property (for maps and structs)
	bodyPropertyRegex = regexp.MustCompile(`^body\.([a-zA-Z0-9_\-]+)(.*)$`)

	// Index access for numeric indices
	numericIndexRegex = regexp.MustCompile(`^(\d+)$`)

	// Index access for last keyword
	lastIndexRegex = regexp.MustCompile(`^last(-(\d+))?$`)

	// "in" operator for list membership
	inOperatorRegex = regexp.MustCompile(`^(.+?)\s+in\s+(.+)$`)

	// "range" operator for range check
	rangeOperatorRegex = regexp.MustCompile(`^(.+?)\s+range\s+(\d+)\.\.(\d+)$`)

	// "is" operator for type checking
	isOperatorRegex = regexp.MustCompile(`^(.+?)\s+is\s+'?(\w+)'?$`)
)

// ParseSimpleExpression parses a simple expression string into an Expression
func ParseSimpleExpression(expression string) (Expression, error) {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		return nil, err
	}
	return template, nil
}

// simpleTemplateCache caches parsed templates keyed by expression text.
//
// Expressions normally come from trusted route configuration (a small, fixed
// set), but nothing stops an application from interpolating per-message text
// through the Simple language. An unbounded sync.Map would then leak one entry
// per distinct expression for the lifetime of the process. simpleCacheSize
// bounds the cache; once the cap is reached, new expressions are parsed on
// every call (the common trusted-config case keeps hitting the cache, since
// its entries were inserted first).
var simpleTemplateCache sync.Map

// MaxSimpleTemplateCacheEntries caps how many parsed Simple templates are
// memoised. 0 disables the cache entirely. Lower it for long-running processes
// that feed unbounded dynamic expressions through the Simple language; raise
// it for applications with a very large but static set of route expressions.
var MaxSimpleTemplateCacheEntries = 10_000

var simpleCacheSize atomic.Int64

func cacheSimpleTemplate(expression string, template *SimpleTemplate) {
	if MaxSimpleTemplateCacheEntries <= 0 {
		return // cache disabled
	}
	if simpleCacheSize.Load() >= int64(MaxSimpleTemplateCacheEntries) {
		// At capacity: parsing is cheap and idempotent, so simply skip the
		// insert rather than implementing eviction over sync.Map. Trusted
		// config (inserted first) stays cached; dynamic overflow pays a
		// re-parse per call instead of growing without bound.
		return
	}
	if _, loaded := simpleTemplateCache.LoadOrStore(expression, template); !loaded {
		simpleCacheSize.Add(1)
	}
}

// ParseSimpleTemplate parses a simple expression template.
//
// SECURITY: expressions larger than MaxSimpleInputBytes are rejected.
// This caps the cost of regex evaluation by the dozens of compiled patterns
// used by the simple language and prevents denial-of-service through
// crafted templates.
func ParseSimpleTemplate(expression string) (*SimpleTemplate, error) {
	if len(expression) > MaxSimpleInputBytes {
		return nil, fmt.Errorf("simple template exceeds MaxSimpleInputBytes (%d > %d)", len(expression), MaxSimpleInputBytes)
	}

	// Try reading from cache
	if val, ok := simpleTemplateCache.Load(expression); ok {
		return val.(*SimpleTemplate), nil
	}

	template := &SimpleTemplate{
		expression: expression,
		parts:      make([]templatePart, 0),
	}

	// Check if it's a simple literal (no placeholders)
	if !placeholderRegex.MatchString(expression) {
		template.isLiteral = true
		template.parts = append(template.parts, templatePart{isVariable: false, content: expression})
		cacheSimpleTemplate(expression, template)
		return template, nil
	}

	// Parse the template and extract placeholders
	lastIndex := 0
	matches := placeholderRegex.FindAllStringIndex(expression, -1)
	for _, match := range matches {
		start, end := match[0], match[1]

		// Add literal text before the placeholder
		if start > lastIndex {
			template.parts = append(template.parts, templatePart{
				isVariable: false,
				content:    expression[lastIndex:start],
			})
		}

		// Extract the expression inside ${...}
		innerExpr := expression[start+2 : end-1]
		template.parts = append(template.parts, templatePart{
			isVariable: true,
			content:    strings.TrimSpace(innerExpr),
		})

		lastIndex = end
	}

	// Add remaining literal text
	if lastIndex < len(expression) {
		template.parts = append(template.parts, templatePart{
			isVariable: false,
			content:    expression[lastIndex:],
		})
	}

	cacheSimpleTemplate(expression, template)
	return template, nil
}

// Evaluate evaluates the template and returns the result
func (t *SimpleTemplate) Evaluate(exchange *Exchange) (interface{}, error) {
	if t.isLiteral {
		return t.parts[0].content, nil
	}

	// Build result from parts
	var result strings.Builder
	for _, part := range t.parts {
		if part.isVariable {
			value, err := evaluateVariable(part.content, exchange)
			if err != nil {
				return nil, err
			}
			result.WriteString(fmt.Sprintf("%v", value))
		} else {
			result.WriteString(part.content)
		}
	}

	return result.String(), nil
}

// EvaluateAsString evaluates the template and returns a string
func (t *SimpleTemplate) EvaluateAsString(exchange *Exchange) (string, error) {
	result, err := t.Evaluate(exchange)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result), nil
}

// EvaluateAsInt evaluates the template and returns an integer
func (t *SimpleTemplate) EvaluateAsInt(exchange *Exchange) (int, bool) {
	result, err := t.Evaluate(exchange)
	if err != nil {
		return 0, false
	}

	switch v := result.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int(f), true
		}
	}

	return 0, false
}

// EvaluateAsBool evaluates the template and returns a boolean
func (t *SimpleTemplate) EvaluateAsBool(exchange *Exchange) (bool, error) {
	result, err := t.Evaluate(exchange)
	if err != nil {
		return false, err
	}

	// Handle boolean directly
	if b, ok := result.(bool); ok {
		return b, nil
	}

	// Convert string result to boolean
	if s, ok := result.(string); ok {
		s = strings.ToLower(strings.TrimSpace(s))
		// Check for explicit boolean strings
		if s == "true" || s == "1" || s == "yes" {
			return true, nil
		}
		if s == "false" || s == "0" || s == "no" || s == "" {
			return false, nil
		}
		// Try parsing as number
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n != 0, nil
		}
		// Non-numeric, non-boolean strings are considered truthy
		return len(s) > 0 && s != "<nil>", nil
	}

	// Handle numeric result
	if n, ok := result.(float64); ok {
		return n != 0, nil
	}
	if n, ok := result.(int); ok {
		return n != 0, nil
	}

	return result != nil, nil
}

// generateUUID generates a simple UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant is 10
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
