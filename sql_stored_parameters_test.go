package gocamel

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestStoredProcedureParamDirection_String(t *testing.T) {
	assert.Equal(t, "IN", ParamDirectionIn.String())
	assert.Equal(t, "OUT", ParamDirectionOut.String())
	assert.Equal(t, "INOUT", ParamDirectionInOut.String())
}

func TestStoredProcedureParam_Creation(t *testing.T) {
	param := StoredProcedureParam{
		Name:      "userId",
		Direction: ParamDirectionIn,
		Value:     42,
		SQLType:   "INT",
	}
	
	assert.Equal(t, "userId", param.Name)
	assert.Equal(t, ParamDirectionIn, param.Direction)
	assert.Equal(t, 42, param.Value)
	assert.Equal(t, "INT", param.SQLType)
}
