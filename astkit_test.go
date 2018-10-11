package astkit_test

import (
	"testing"

	"context"

	"github.com/gokit/astkit"
	"github.com/stretchr/testify/assert"
)

func TestTransform(t *testing.T) {
	sudo, others, err := astkit.Transform(context.Background(), "github.com/gokit/astkit/testbed/sudo")
	assert.NoError(t, err)
	assert.NotNil(t, sudo)
	assert.NotNil(t, others)
	assert.NotNil(t, others["fmt"])
	assert.NotNil(t, others["runtime"])
	assert.NotNil(t, others["github.com/gokit/astkit/testbed/sudo"])
	assert.NotNil(t, others["github.com/gokit/astkit/testbed/sudo/api"])

	functioner, err := sudo.GetInterface("Functioner", "")
	assert.NoError(t, err)
	assert.NotNil(t, functioner)

	pengu, err := sudo.GetVariable("pengu", "")
	assert.NoError(t, err)
	assert.NotNil(t, pengu)
	assert.NotNil(t, pengu.Value)

	pengu2, err := sudo.GetVariable("pengu2", "")
	assert.NoError(t, err)
	assert.NotNil(t, pengu2)
	assert.NotNil(t, pengu2.Value)

	pengu3, err := sudo.GetVariable("pengu3", "")
	assert.NoError(t, err)
	assert.NotNil(t, pengu3)
	assert.NotNil(t, pengu3.Value)
t git 
	mko, err := sudo.GetVariable("mko", "")
	assert.NoError(t, err)
	assert.NotNil(t, mko)
	assert.Nil(t, mko.Value)
	assert.NotNil(t, mko.Type)

	one, err := sudo.GetVariable("one", "")
	assert.NoError(t, err)
	assert.NotNil(t, one)
	assert.Nil(t, one.Type)
	assert.NotNil(t, one.Value)
	assert.NotNil(t, one.Value.ID(), "1")

	fullFN, err := sudo.GetInterface("FullFn", "")
	assert.NoError(t, err)
	assert.NotNil(t, fullFN)

	fnreco, err := sudo.GetStruct("FunctionReco", "")
	assert.NoError(t, err)
	assert.NotNil(t, fnreco)

	fnres, err := sudo.GetStruct("FunctionResult", "")
	assert.NoError(t, err)
	assert.NotNil(t, fnres)
	assert.NotNil(t, fnres.Fields["Field"])
	assert.NotNil(t, fnres.Fields["Day"])
	assert.Equal(t, "int", fnres.Fields["Day"].Type.ID())
	assert.Equal(t, "string", fnres.Fields["Field"].Type.ID())
	assert.NotNil(t, fnres.Methods["hello"])
	assert.Equal(t, "fn", fnres.Methods["hello"].Arguments[0].Name)
}
