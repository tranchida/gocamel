# Simple Language Documentation

GoCamel's Simple Language provides a powerful expression language for evaluating and manipulating data in your routes.

## Table of Contents

1. [Basic Variable Access](#basic-variable-access)
2. [String Operations](#string-operations)
3. [Logical Operators](#logical-operators)
4. [Ternary Operator](#ternary-operator)
5. [String Functions](#string-functions)
6. [Math Operations](#math-operations)
7. [Type/Range Operations](#typerange-operations)
8. [Function Chaining](#function-chaining)
9. [Null-Safe Operations](#null-safe-operations)

## Basic Variable Access

### Body Access
```go
${body}                           // Access the message body
${body.field}                     // Access a map/struct field
${body['key']}                    // Access via bracket notation
${body?.field}                    // Null-safe property access
${body[0]}                        // Access array/slice element
${body[last]}                     // Access last element
${body[last-1]}                   // Access second-to-last element
```

### Header Access
```go
${header.name}                    // Access header by name
${header['name']}                 // Access header via bracket notation
${header?.name}                   // Null-safe header access
${header['X-Custom-Header']}      // Headers with special characters
```

### Exchange Property Access
```go
${exchangeProperty.name}          // Access exchange property
${exchangeProperty['name']}       // Access via bracket notation
${exchangeProperty?.name}         // Null-safe property access
```

## String Operations

### Contains
Checks if the string contains a substring:
```go
${body contains 'text'}           // Returns: true/false
${header.message contains 'urgent'}
```

### StartsWith
Checks if the string starts with a prefix:
```go
${body startsWith 'prefix'}       // Returns: true/false
${header.path startsWith '/api'}
```

### EndsWith
Checks if the string ends with a suffix:
```go
${body endsWith '.json'}          // Returns: true/false
${header.filename endsWith '.xml'}
```

### Regex
Checks if the string matches a regular expression pattern:
```go
${body regex '\\d+'}              // Match digits
${header.email regex '^[\\w.]+@[\\w.]+\\.[a-z]+$'}
```

## Logical Operators

### AND (&&)
All conditions must be true:
```go
${header.count > 5 && header.type == 'gold'}
${body contains 'URGENT' && header.priority == 'high'}
```

### OR (||)
At least one condition must be true:
```go
${header.count > 10 || body contains 'urgent'}
${header.status == 'active' || header.status == 'pending'}
```

### NOT (!)
Negates a condition:
```go
${!header.processed}
${!exchangeProperty.skip}
${!(header.priority == 'low')}
```

### Operator Precedence
1. `()` (parentheses) - highest precedence
2. `!` (NOT)
3. `&&` (AND)
4. `||` (OR) - lowest precedence

```go
// Example: NOT has higher precedence than AND/OR
${!header.processed && header.active}
// Evaluated as: (!header.processed) && header.active

// Example: AND has higher precedence than OR
${header.a || header.b && header.c}
// Evaluated as: header.a || (header.b && header.c)
```

## Ternary Operator

The ternary operator provides a compact way to write conditional expressions:

```go
${condition ? true-value : false-value}
```

### Examples
```go
${header.type == 'gold' ? 'Premium' : 'Standard'}
${header.amount > 100 ? 'High' : 'Low'}
${body contains 'URGENT' ? 'Fast' : 'Normal'}
```

## String Functions

### trim()
Removes leading and trailing whitespace:
```go
${body.trim()}
// Input: "  Hello World  " → Output: "Hello World"
```

### uppercase() / upper()
Converts to uppercase:
```go
${body.uppercase()}
${header.name.upper()}
// Input: "hello" → Output: "HELLO"
```

### lowercase() / lower()
Converts to lowercase:
```go
${body.lowercase()}
${header.name.lower()}
// Input: "HELLO" → Output: "hello"
```

### size() / length()
Returns the length of a string, slice, or map:
```go
${body.size()}                    // Length of string
${header.items.length()}          // Length of slice/array
${body.mapField.size()}          // Size of map
```

### substring(start, [end])
Extracts a substring:
```go
${body.substring(6)}              // From index 6 to end
${body.substring(0, 5)}           // From index 0 to 4
// Input: "Hello World"
// substring(6) → "World"
// substring(0, 5) → "Hello"
```

### replace(old, new)
Replaces all occurrences:
```go
${body.replace('old', 'new')}
// Input: "Hello World World"
// Output: "Hello Universe Universe"
```

### split(delimiter, [limit])
Splits a string into a slice using an optional delimiter (default: `,`) and an optional limit on the number of parts:
```go
${body.split(',')}                // Split by comma
${body.split(';', 2)}             // Split by ';' into at most 2 parts
${body.split()}                   // Split by default comma delimiter
// Input: "a,b,c,d" → split(',') → ["a", "b", "c", "d"]
// Input: "a;b;c;d" → split(';', 2) → ["a", "b;c;d"]
```

### normalizeWhitespace()
Replaces tabs, newlines and carriage returns with spaces, collapses multiple whitespace characters into a single space, and trims leading/trailing whitespace:
```go
${body.normalizeWhitespace()}
// Input: "  Hello\t\tWorld\n\n  Foo  " → Output: "Hello World Foo"
```

## Math Operations

### Addition (+)
```go
${header.count + 10}
${10 + 5}  // → 15
```

### Subtraction (-)
```go
${header.count - 5}
${20 - 8}  // → 12
```

### Multiplication (*)
```go
${header.count * 2}
${5 * 4}   // → 20
```

### Division (/)
```go
${header.count / 2}
${10 / 2}  // → 5
```

### Modulo (%)
```go
${header.count % 2}
${10 % 3}  // → 1
```

### Operator Precedence
Math operations follow standard precedence:
1. `*`, `/`, `%` (left to right)
2. `+`, `-` (left to right)

```go
${2 + 3 * 4}     // → 14 (not 20)
${(2 + 3) * 4}   // → 20 (parentheses override)
```

## Type/Range Operations

### "in" Operator - List Membership
Checks if a value is in a comma-separated list:
```go
${header.type in 'gold,silver,bronze'}
${header.status in 'pending,processing,completed'}
```

### "range" Operator - Range Check
Checks if a number is within a range:
```go
${header.count range 100..199}    // 100 ≤ count ≤ 199
${header.code range 200..299}     // 200 ≤ code ≤ 299
```

### "is" Operator - Type Checking
Checks the type of a value:
```go
${body is 'String'}               // Is string?
${body is 'Int'}                  // Is integer?
${header.data is 'Map'}           // Is map?
${header.items is 'Slice'}        // Is slice/array?
${header.flag is 'Bool'}          // Is boolean?
```

Supported type names:
- `String`, `string`
- `Int`, `Integer`, `int`
- `Float`, `Double`, `Number`, `float64`
- `Bool`, `Boolean`, `bool`
- `Map`, `map`
- `Slice`, `Array`, `List`

## Function Chaining

String functions can be chained together:

```go
${body.trim().uppercase()}              // "  hello  " → "HELLO"
${body.trim().uppercase().size()}       // "  hello  " → "5"
${header.name.trim().lowercase().substring(0,5)}
```

### Chain Evaluation
1. Evaluate base expression (e.g., `body`)
2. Apply first function (e.g., `trim()`)
3. Apply second function to result (e.g., `uppercase()`)
4. Continue for each function in the chain

## Null-Safe Operations

The null-safe operator (`?.`) allows accessing properties without throwing errors on nil values:

```go
${body?.field}                    // Returns nil if body is nil
${body?.field?.subfield}          // Chain multiple null-safe accesses
${header?.name}                   // Returns nil if header doesn't exist
${exchangeProperty?.name}         // Returns nil if property doesn't exist
```

### Null-Safe Function Chaining
```go
${body?.trim()}                   // Returns nil if body is nil
${body?.trim().uppercase()}       // Returns nil if body is nil
```

## Built-in Functions

### Date Functions
```go
${date:now}                       // Current date/time in RFC3339 format
${date:now:2006-01-02}           // Current date with custom format
```

### Random Function
```go
${random(100)}                    // Random number 0-99
```

### UUID Function
```go
${uuid}                           // Generate UUID v4
```

## Complex Expressions

Combine multiple operations for powerful expressions:

```go
// Ternary with contains
${body contains 'URGENT' ? 'Fast' : 'Normal'}

// Logical operators with string operations
${body contains 'URGENT' && header.amount > 100 || body startsWith 'CRITICAL'}

// Function in ternary
${header.code == 'VIP' ? body.substring(0,10).uppercase() : 'Standard'}

// Multiple conditions in Choice
.Choice().
    When("${header.priority == 'high' && header.amount > 100}").
    SetBody("High priority transaction").
    When("${body contains 'URGENT' || body startsWith 'CRITICAL'}").
    SetBody("Urgent request").
    When("${header.category in 'A,B,C'}").
    SetBody("Category A, B, or C").
    Otherwise().
    SetBody("Standard").
    EndChoice()
```

## Error Handling

The Simple Language handles errors gracefully:

- **Nil values**: Return `<nil>` or evaluate to `false`
- **Missing properties**: Return `<nil>`
- **Invalid regex**: Returns `false` with the error
- **Division by zero**: Returns an error
- **Type mismatches**: Attempts coercion or returns appropriate default

## Performance Considerations

Expressions are parsed once and cached. For optimal performance:

1. Parse expressions once and reuse the template
2. Use simple accessors when possible
3. Avoid deeply nested expressions
4. Use bracket notation for dynamic field access
5. Chain functions instead of multiple separate calls
