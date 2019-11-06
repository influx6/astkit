package compiler_test

import (
	"runtime"
	"testing"

	"github.com/influx6/astkit/compiler"
	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	program, err := compiler.Load(&compiler.Cg{
		Internals: []string{
			"expvars",
			"archive",
		},
		Imports: []string{
			"github.com/influx6/es",
		},
	}, "github.com/influx6/astkit/testbed/sudo", runtime.GOARCH, runtime.GOOS)
	assert.NoError(t, err)
	assert.NotNil(t, program)
	assert.NotNil(t, program.Package("github.com/influx6/astkit/testbed/sudo"))
	assert.NotNil(t, program.Package("github.com/influx6/astkit/testbed/sudo/api"))
}
