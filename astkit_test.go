package astkit_test

import (
	"testing"

	"context"

	"github.com/stretchr/testify/assert"
	"github.com/gokit/astkit"
)

func TestTransform(t *testing.T) {
	pkg, others, err := astkit.Transform(context.Background(), "gitlab.com/gokit/astkit/testbed/sudo")
	assert.NoError(t, err)
	assert.NotNil(t, pkg)
	assert.NotNil(t, others)

}
