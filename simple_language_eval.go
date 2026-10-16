package gocamel

import (
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
)

// This file holds the evaluateVariable dispatcher and the small helpers that
// support it: math-operator splitting, function-call detection, function
// chaining and logical-operator splitting. The regex patterns used here are
// all declared in simple_language.go.

// evaluateVariable evaluates a single variable expression
func evaluateVariable(expr string, exchange *Exchange) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	if expr == "" {
		return nil, nil
	}

	// Check if it's a numeric literal - check very early to avoid confusion with math ops or dots
	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}

	// Check if it's a literal string (quoted)
	if (strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) ||
		(strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) {
		return strings.Trim(expr, "'\""), nil
	}

	// Check for simple variable access patterns FIRST
	if result, err := evaluateSimpleVariableAccess(expr, exchange); err == nil {
		return result, nil
	}

	// Check for function chaining: e.g., body.trim(), header.name.uppercase()
	if idx := findFirstMethodCall(expr); idx > 0 {
		base := strings.TrimSpace(expr[:idx])
		if !strings.ContainsAny(base, " ?><=!|&+-*/%:") {
			result, err := evaluateFunctionChain(expr, exchange)
			if err == nil {
				return result, nil
			}
			if !strings.Contains(err.Error(), "unexpected trailing characters") &&
				!strings.Contains(err.Error(), "not a function chain base") {
				return nil, err
			}
		}
	}

	// Check for header. and exchangeProperty. patterns (avoid matching as math operation)
	if strings.HasPrefix(expr, "header.") || strings.HasPrefix(expr, "exchangeProperty.") {
		if match := dotNotationRegex.FindStringSubmatch(expr); match != nil {
			return evaluateDotNotationRegex(match, exchange)
		}
	}

	// Now handle operators and complex expressions

	// Check for date function - check very early since date format looks like subtraction
	if dateMatch := dateFunctionRegex.FindStringSubmatch(expr); dateMatch != nil {
		return evaluateDateFunction(dateMatch[1], dateMatch[2], dateMatch[3], exchange)
	}

	// Check for file function
	if fileMatch := fileFunctionRegex.FindStringSubmatch(expr); fileMatch != nil {
		return evaluateFileFunction(fileMatch[1], exchange)
	}

	// Check for math function
	if mathMatch := mathFunctionRegex.FindStringSubmatch(expr); mathMatch != nil {
		return evaluateVariable(mathMatch[1], exchange)
	}

	// Check for numeric function
	if numMatch := numericFunctionRegex.FindStringSubmatch(expr); numMatch != nil {
		return evaluateNumericFunction(numMatch[1], numMatch[2], exchange)
	}

	// Check for base64 function
	if b64Match := base64FunctionRegex.FindStringSubmatch(expr); b64Match != nil {
		return evaluateBase64Function(b64Match[1], b64Match[2], exchange)
	}

	// Check for random function
	if randomMatch := randomFunctionRegex.FindStringSubmatch(expr); randomMatch != nil {
		max, err := strconv.Atoi(randomMatch[1])
		if err != nil {
			return nil, fmt.Errorf("invalid random argument: %s", randomMatch[1])
		}
		if max <= 0 {
			return nil, fmt.Errorf("random argument must be positive, got %d", max)
		}
		// Deliberately non-cryptographic: this is the Camel-compatible
		// ${random(n)} helper for test/demo values, not a secret source
		// (gosec G404).
		return rand.Intn(max), nil // #nosec G404
	}

	// Check for uuid function
	if uuidFunctionRegex.MatchString(expr) {
		return generateUUID(), nil
	}

	// Handle NOT operator (!expr) - highest precedence
	if strings.HasPrefix(expr, "!") {
		innerExpr := strings.TrimSpace(expr[1:])
		innerVal, err := evaluateVariable(innerExpr, exchange)
		if err != nil {
			return nil, err
		}
		return !toBool(innerVal), nil
	}

	// Handle parenthesized expressions first
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		inner := expr[1 : len(expr)-1]
		return evaluateVariable(inner, exchange)
	}

	// Handle ternary operator first (lowest precedence)
	if ternaryMatch := ternaryRegex.FindStringSubmatch(expr); ternaryMatch != nil {
		condition := strings.TrimSpace(ternaryMatch[1])
		trueVal := strings.TrimSpace(ternaryMatch[2])
		falseVal := strings.TrimSpace(ternaryMatch[3])

		condResult, err := evaluateVariable(condition, exchange)
		if err != nil {
			return nil, err
		}

		if toBool(condResult) {
			return evaluateVariable(trueVal, exchange)
		}
		return evaluateVariable(falseVal, exchange)
	}

	// Handle logical OR (||) - lower precedence than AND
	if orParts := splitLogicalOr(expr); len(orParts) > 1 {
		// Evaluate first part
		leftResult, err := evaluateVariable(orParts[0], exchange)
		if err != nil {
			return nil, err
		}
		if toBool(leftResult) {
			return true, nil // Short-circuit
		}
		// Evaluate remaining parts
		for i := 1; i < len(orParts); i++ {
			result, err := evaluateVariable(orParts[i], exchange)
			if err != nil {
				return nil, err
			}
			if toBool(result) {
				return true, nil
			}
		}
		return false, nil
	}

	// Handle logical AND (&&) - higher precedence than OR
	if andParts := splitLogicalAnd(expr); len(andParts) > 1 {
		// Evaluate all parts - short circuit on first false
		for _, part := range andParts {
			result, err := evaluateVariable(part, exchange)
			if err != nil {
				return nil, err
			}
			if !toBool(result) {
				return false, nil // Short-circuit
			}
		}
		return true, nil
	}

	// Handle "is" operator for type checking
	if isMatch := isOperatorRegex.FindStringSubmatch(expr); isMatch != nil {
		left := strings.TrimSpace(isMatch[1])
		typeName := isMatch[2]

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		return checkType(leftVal, typeName), nil
	}

	// Handle "in" operator for list membership
	if inMatch := inOperatorRegex.FindStringSubmatch(expr); inMatch != nil {
		left := strings.TrimSpace(inMatch[1])
		listStr := strings.TrimSpace(inMatch[2])

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		// Parse the list from the comma-separated string
		// Remove quotes if present
		listStr = strings.Trim(listStr, "'\"")
		items := strings.Split(listStr, ",")
		for i, item := range items {
			items[i] = strings.TrimSpace(item)
		}

		return containsValue(items, leftVal), nil
	}

	// Handle "range" operator for range check
	if rangeMatch := rangeOperatorRegex.FindStringSubmatch(expr); rangeMatch != nil {
		left := strings.TrimSpace(rangeMatch[1])
		minVal, _ := strconv.Atoi(rangeMatch[2])
		maxVal, _ := strconv.Atoi(rangeMatch[3])

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		numVal := toFloat64(leftVal)
		return numVal >= float64(minVal) && numVal <= float64(maxVal), nil
	}

	// Handle comparison operators
	if compMatch := comparisonRegex.FindStringSubmatch(expr); compMatch != nil {
		left := strings.TrimSpace(compMatch[1])
		op := compMatch[2]
		right := strings.TrimSpace(compMatch[3])

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		rightVal, err := evaluateVariable(right, exchange)
		if err != nil {
			// Try to parse as literal
			if strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'") {
				rightVal = strings.Trim(right, "'")
			} else if strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"") {
				rightVal = strings.Trim(right, "\"")
			} else {
				return nil, err
			}
		}

		return compareValues(leftVal, op, rightVal)
	}

	// Handle string operations: contains, startsWith, endsWith, regex
	if strOpMatch := stringOpRegex.FindStringSubmatch(expr); strOpMatch != nil {
		left := strings.TrimSpace(strOpMatch[1])
		op := strOpMatch[2]
		right := strings.TrimSpace(strOpMatch[3])

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		// Parse right operand as string literal or variable
		var rightVal string
		if (strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'")) ||
			(strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"")) {
			rightVal = strings.Trim(right, "'\"")
		} else {
			rv, err := evaluateVariable(right, exchange)
			if err != nil {
				return nil, err
			}
			rightVal = fmt.Sprintf("%v", rv)
		}

		leftStr := fmt.Sprintf("%v", leftVal)
		switch op {
		case "contains":
			return strings.Contains(leftStr, rightVal), nil
		case "startsWith":
			return strings.HasPrefix(leftStr, rightVal), nil
		case "endsWith":
			return strings.HasSuffix(leftStr, rightVal), nil
		case "regex":
			if len(rightVal) > MaxSimpleInputBytes {
				return false, fmt.Errorf("regex pattern length %d exceeds MaxSimpleInputBytes (%d)", len(rightVal), MaxSimpleInputBytes)
			}
			re, err := regexp.Compile(rightVal)
			if err != nil {
				return false, fmt.Errorf("invalid regex pattern: %w", err)
			}
			return re.MatchString(leftStr), nil
		}
	}

	// Handle math operations (precedence: + - then * / %)
	// We split by + and - first (outermost level)
	if left, right, op := splitMath(expr, "+", "-"); op != "" {
		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}
		rightVal, err := evaluateVariable(right, exchange)
		if err != nil {
			return nil, err
		}
		if op == "+" {
			return toFloat64(leftVal) + toFloat64(rightVal), nil
		}
		return toFloat64(leftVal) - toFloat64(rightVal), nil
	}

	if left, right, op := splitMath(expr, "*", "/", "%"); op != "" {
		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}
		rightVal, err := evaluateVariable(right, exchange)
		if err != nil {
			return nil, err
		}
		l, r := toFloat64(leftVal), toFloat64(rightVal)
		switch op {
		case "*":
			return l * r, nil
		case "/":
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return l / r, nil
		case "%":
			if r == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return math.Mod(l, r), nil
		}
	}

	// Try variable access patterns as a fallback (after operators have been processed)
	if result, err := evaluateSimpleVariableAccess(expr, exchange); err == nil {
		return result, nil
	}

	// Check for variable access patterns - now more flexible
	if match := dotNotationRegex.FindStringSubmatch(expr); match != nil {
		return evaluateDotNotationRegex(match, exchange)
	}

	// Check for jsonpath function
	if jpMatch := jsonpathFunctionRegex.FindStringSubmatch(expr); jpMatch != nil {
		return evaluateJSONPathFunction(jpMatch[1], exchange)
	}

	// Check if it's a boolean literal
	if expr == "true" || expr == "TRUE" {
		return true, nil
	}
	if expr == "false" || expr == "FALSE" {
		return false, nil
	}

	// Legacy: also check header. and exchangeProperty. patterns directly
	if strings.HasPrefix(expr, "header.") {
		name := strings.TrimPrefix(expr, "header.")
		value, exists := exchange.GetHeader(name)
		if !exists {
			return nil, nil
		}
		return value, nil
	}
	if strings.HasPrefix(expr, "exchangeProperty.") {
		name := strings.TrimPrefix(expr, "exchangeProperty.")
		value, exists := exchange.GetProperty(name)
		if !exists {
			return nil, nil
		}
		return value, nil
	}

	return nil, fmt.Errorf("unknown expression: %s", expr)
}

// splitMath splits an expression by the first matching operator (from right to left)
// that is NOT inside parentheses. This respects left-associativity and precedence
// when called in the correct order.
func splitMath(expr string, ops ...string) (string, string, string) {
	depth := 0
	for i := len(expr) - 1; i >= 0; i-- {
		ch := expr[i]
		if ch == ')' {
			depth++
		} else if ch == '(' {
			depth--
		} else if depth == 0 {
			for _, op := range ops {
				if i >= len(op)-1 && expr[i-len(op)+1:i+1] == op {
					// Check if it's really the operator and not part of a name (simplified)
					// For single char ops like +, -, *, /, % this is usually fine if surrounded by spaces or not letters
					return expr[:i-len(op)+1], expr[i+1:], op
				}
			}
		}
	}
	return "", "", ""
}

// findFirstMethodCall finds the index of the first dot that starts a method call
func findFirstMethodCall(expr string) int {
	// Look for .functionName( pattern
	for i := 0; i < len(expr); i++ {
		if expr[i] == '.' {
			// Check if what follows is a method call (function name followed by ()
			for j := i + 1; j < len(expr); j++ {
				if expr[j] == '(' {
					// Found a method call
					return i
				}
				if expr[j] == '.' || expr[j] == '[' || expr[j] == ' ' {
					// Not a method call
					break
				}
			}
		}
	}
	return -1
}

// evaluateFunctionChain evaluates chained function calls like body.trim().uppercase()
func evaluateFunctionChain(expr string, exchange *Exchange) (interface{}, error) {
	// Find the first dot that starts a method call
	dotIdx := findFirstMethodCall(expr)
	if dotIdx == -1 {
		return nil, fmt.Errorf("no function call found in chain: %s", expr)
	}

	// Get the base value
	baseExpr := strings.TrimSpace(expr[:dotIdx])
	if strings.ContainsAny(baseExpr, " ?><=!|&+-*/%:") {
		return nil, fmt.Errorf("not a function chain base: %s", baseExpr)
	}
	rest := expr[dotIdx:]

	baseVal, err := evaluateVariable(baseExpr, exchange)
	if err != nil {
		return nil, err
	}

	// Apply chained functions
	return applyChainedFunctions(baseVal, rest, exchange)
}

// applyChainedFunctions applies chained function calls like .trim().uppercase()
func applyChainedFunctions(currentVal interface{}, chain string, exchange *Exchange) (interface{}, error) {
	if currentVal == nil {
		return nil, nil
	}

	for chain != "" {
		// Must start with .
		if !strings.HasPrefix(chain, ".") {
			break
		}
		chain = chain[1:] // Remove leading .

		// Find function name and arguments
		parenIdx := strings.Index(chain, "(")
		if parenIdx == -1 {
			// No arguments - could be property access, but we're in function chain mode
			return nil, fmt.Errorf("expected function call in chain, got: %s", chain)
		}

		funcName := strings.TrimSpace(chain[:parenIdx])
		if closeIdx := strings.Index(chain, ")"); closeIdx == -1 {
			return nil, fmt.Errorf("unmatched parenthesis in function call: %s", chain)
		} else {
			argsStr := chain[parenIdx+1 : closeIdx]
			chain = chain[closeIdx+1:]

			// Parse arguments
			args := parseFunctionArgs(argsStr, exchange)

			// Apply the function
			result, err := applyStringFunction(funcName, currentVal, args)
			if err != nil {
				return nil, err
			}
			currentVal = result
		}
	}

	if strings.TrimSpace(chain) != "" {
		return nil, fmt.Errorf("unexpected trailing characters in function chain: %s", chain)
	}

	return currentVal, nil
}

// parseFunctionArgs parses function arguments, handling both literals and expressions
func parseFunctionArgs(argsStr string, exchange *Exchange) []interface{} {
	if argsStr == "" {
		return []interface{}{}
	}

	// Split by comma, but handle nested expressions
	var args []interface{}
	for _, part := range splitArgs(argsStr) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Try to evaluate as expression first
		if val, err := evaluateVariable(part, exchange); err == nil {
			args = append(args, val)
		} else {
			// Treat as literal
			args = append(args, part)
		}
	}

	return args
}

// splitArgs splits comma-separated arguments, respecting nested structures
func splitArgs(argsStr string) []string {
	var result []string
	var current strings.Builder
	depth := 0

	for _, ch := range argsStr {
		switch ch {
		case '(':
			depth++
			current.WriteRune(ch)
		case ')':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				result = append(result, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// splitLogicalAnd splits expression by && respecting parentheses
func splitLogicalAnd(expr string) []string {
	return splitByOperator(expr, "&&")
}

// splitLogicalOr splits expression by || respecting parentheses
func splitLogicalOr(expr string) []string {
	return splitByOperator(expr, "||")
}

// splitByOperator splits expression by an operator while respecting parentheses
// and quoted string literals. Tracking quotes prevents a && or || inside a
// quoted value (e.g. ${body == 'a&&b'}) from being mistaken for a logical
// operator — which would otherwise break the literal and could let a
// user-controlled value escape its quotes (expression injection).
func splitByOperator(expr string, op string) []string {
	var result []string
	var current strings.Builder
	depth := 0
	opLen := len(op)
	inSingle := false
	inDouble := false

	for i := 0; i < len(expr); i++ {
		ch := expr[i]

		if inSingle {
			current.WriteByte(ch)
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			current.WriteByte(ch)
			if ch == '"' {
				inDouble = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingle = true
			current.WriteByte(ch)
		case '"':
			inDouble = true
			current.WriteByte(ch)
		case '(':
			depth++
			current.WriteByte(ch)
		case ')':
			depth--
			current.WriteByte(ch)
		default:
			if depth == 0 && i+opLen <= len(expr) && expr[i:i+opLen] == op {
				result = append(result, strings.TrimSpace(current.String()))
				current.Reset()
				i += opLen - 1 // Skip the rest of operator
			} else {
				current.WriteByte(ch)
			}
		}
	}

	if current.Len() > 0 {
		result = append(result, strings.TrimSpace(current.String()))
	}

	return result
}
