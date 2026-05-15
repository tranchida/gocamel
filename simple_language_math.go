package gocamel

import (
	"fmt"
	"math"
	"strconv"
)

// This file holds the numeric coercion helper (toFloat64) used by the math
// operators in evaluateVariable, and the implementation of the math.* Simple
// Language functions (round, floor, ceil, abs, sqrt, sin, cos, tan).

// toFloat64 converts a value to float64
func toFloat64(val interface{}) float64 {
	if val == nil {
		return 0
	}

	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case string:
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	}

	return 0
}

// evaluateNumericFunction evaluates numeric functions like round, floor, ceil, etc.
func evaluateNumericFunction(fn, arg string, exchange *Exchange) (interface{}, error) {
	val, err := evaluateVariable(arg, exchange)
	if err != nil {
		return nil, err
	}

	num := toFloat64(val)

	switch fn {
	case "round":
		return math.Round(num), nil
	case "floor":
		return math.Floor(num), nil
	case "ceil":
		return math.Ceil(num), nil
	case "abs":
		return math.Abs(num), nil
	case "sqrt":
		return math.Sqrt(num), nil
	case "sin":
		return math.Sin(num), nil
	case "cos":
		return math.Cos(num), nil
	case "tan":
		return math.Tan(num), nil
	default:
		return nil, fmt.Errorf("unknown numeric function: %s", fn)
	}
}
