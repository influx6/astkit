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

}
