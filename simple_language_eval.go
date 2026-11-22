package gocamel

import (
	"fmt"
	"strings"
)

// This file holds the evaluateVariable entry point (backed by the recursive-descent
// parser in simple_language_parser.go) and the chained-method helper functions.

// evaluateVariable evaluates a single variable expression.
func evaluateVariable(expr string, exchange *Exchange) (interface{}, error) {
	return parseAndEvaluateSimple(expr, exchange)
}

// findClosingParen finds the matching ')' for the '(' at openIdx, respecting
// single/double quotes and nested parentheses.
func findClosingParen(s string, openIdx int) (int, error) {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '(' {
		return -1, fmt.Errorf("expected '(' in function call: %s", s)
	}
	depth := 0
	inSingle := false
	inDouble := false
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if inSingle {
			if ch == '\\' && i+1 < len(s) && (s[i+1] == '\'' || s[i+1] == '\\') {
				i++
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("unmatched parenthesis in function call: %s", s)
}

func isValidMethodName(name string) bool {
	if name == "" || !isIdentStartByte(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isIdentContByte(name[i]) {
			return false
		}
	}
	return true
}

// applyChainedFunctions applies chained function calls like .trim().uppercase().
// Chain syntax and trailing characters are validated even when currentVal is nil.
func applyChainedFunctions(currentVal interface{}, chain string, exchange *Exchange) (interface{}, error) {
	for chain != "" {
		if !strings.HasPrefix(chain, ".") {
			break
		}
		chain = chain[1:] // Remove leading .

		parenIdx := strings.IndexByte(chain, '(')
		if parenIdx == -1 {
			return nil, fmt.Errorf("expected function call in chain, got: %s", chain)
		}

		funcName := strings.TrimSpace(chain[:parenIdx])
		if !isValidMethodName(funcName) {
			return nil, fmt.Errorf("invalid function name in chain: %s", funcName)
		}

		closeIdx, err := findClosingParen(chain, parenIdx)
		if err != nil {
			return nil, err
		}

		argsStr := chain[parenIdx+1 : closeIdx]
		chain = chain[closeIdx+1:]

		args, err := parseFunctionArgs(argsStr, exchange)
		if err != nil {
			return nil, err
		}

		result, err := applyStringFunction(funcName, currentVal, args)
		if err != nil {
			return nil, err
		}
		currentVal = result
	}

	if strings.TrimSpace(chain) != "" {
		return nil, fmt.Errorf("unexpected trailing characters in function chain: %s", chain)
	}

	return currentVal, nil
}

// parseFunctionArgs parses function arguments, handling both literals and expressions.
func parseFunctionArgs(argsStr string, exchange *Exchange) ([]interface{}, error) {
	if strings.TrimSpace(argsStr) == "" {
		return []interface{}{}, nil
	}

	parts, err := splitArgs(argsStr)
	if err != nil {
		return nil, err
	}

	args := make([]interface{}, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if val, err := evaluateVariable(part, exchange); err == nil {
			args = append(args, val)
		} else {
			args = append(args, part)
		}
	}

	return args, nil
}

// splitArgs splits comma-separated arguments, respecting quotes, parentheses, and brackets.
func splitArgs(argsStr string) ([]string, error) {
	var result []string
	var current strings.Builder
	parenDepth := 0
	bracketDepth := 0
	inSingle := false
	inDouble := false

	for i := 0; i < len(argsStr); i++ {
		ch := argsStr[i]
		if inSingle {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(argsStr) && (argsStr[i+1] == '\'' || argsStr[i+1] == '\\') {
				i++
				current.WriteByte(argsStr[i])
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(argsStr) && (argsStr[i+1] == '"' || argsStr[i+1] == '\\') {
				i++
				current.WriteByte(argsStr[i])
				continue
			}
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
			parenDepth++
			current.WriteByte(ch)
		case ')':
			parenDepth--
			if parenDepth < 0 {
				return nil, fmt.Errorf("unexpected ')' in arguments: %s", argsStr)
			}
			current.WriteByte(ch)
		case '[':
			bracketDepth++
			current.WriteByte(ch)
		case ']':
			bracketDepth--
			if bracketDepth < 0 {
				return nil, fmt.Errorf("unexpected ']' in arguments: %s", argsStr)
			}
			current.WriteByte(ch)
		case ',':
			if parenDepth == 0 && bracketDepth == 0 {
				result = append(result, current.String())
				current.Reset()
			} else {
				current.WriteByte(ch)
			}
		default:
			current.WriteByte(ch)
		}
	}

	if inSingle || inDouble || parenDepth != 0 || bracketDepth != 0 {
		return nil, fmt.Errorf("unbalanced quotes or delimiters in arguments: %s", argsStr)
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result, nil
}
