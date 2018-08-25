package astkit_test

import (
	"testing"

	"context"

	"github.com/gokit/astkit"
	"github.com/stretchr/testify/assert"
)

func TestTransform(t *testing.T) {
	pkg, others, err := astkit.Transform(context.Background(), "github.com/gokit/astkit/testbed/sudo")
	assert.NoError(t, err)
	assert.NotNil(t, pkg)
	assert.NotNil(t, others)
}
