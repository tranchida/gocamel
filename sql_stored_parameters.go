package gocamel

// StoredProcedureParamDirection indicates parameter flow direction
type StoredProcedureParamDirection int

const (
	ParamDirectionIn    StoredProcedureParamDirection = iota // IN parameter
	ParamDirectionOut                                          // OUT parameter  
	ParamDirectionInOut                                        // INOUT parameter
)

// StoredProcedureParam represents a stored procedure parameter
type StoredProcedureParam struct {
	Name      string                        // Parameter name (for named parameters)
	Direction StoredProcedureParamDirection // IN, OUT, or INOUT
	Value     interface{}                   // Input value (for IN/INOUT)
	SQLType   string                        // SQL type hint (optional, e.g., "VARCHAR", "INT")
}

// String representation for debugging
func (d StoredProcedureParamDirection) String() string {
	switch d {
	case ParamDirectionIn:
		return "IN"
	case ParamDirectionOut:
		return "OUT"
	case ParamDirectionInOut:
		return "INOUT"
	}
	return "UNKNOWN"
}
