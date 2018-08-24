package compiler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/gokit/astkit/compiler"
)

func TestLoad(t *testing.T) {
	program, err := compiler.Load("github.com/gokit/astkit/testbed/sudo", compiler.Cg{
		Internals: []string{
			"expvars",
			"archive",
		},
		Imports: []string{
			"github.com/gokit/es",
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, program)
}
