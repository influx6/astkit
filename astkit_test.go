package astkit_test

import (
	"fmt"
	"testing"

	"context"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gokit/astkit"
)

func TestTransform(t *testing.T) {
	pkg, err := astkit.Transform(context.Background(), "gitlab.com/gokit/astkit/testbed/sudo")
	assert.NoError(t, err)
	assert.NotNil(t, pkg)

	fmt.Printf("Packge: %#v", pkg)
}
