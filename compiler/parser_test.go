package compiler_test

import (
	"bytes"
	"testing"

	"github.com/gokit/astkit/compiler"
	"github.com/stretchr/testify/assert"
)

// TestAnnotationParserWithSingleComments validates the behaviour of the comment parsing method for
// parsing comments annotations.
func TestAnnotationParserWithSingleComments(t *testing.T) {
	reader := bytes.NewBufferString(`//
// The reason we wish to create this system is to allow the ability to parse such
// details outside of our scope.
//  @terminate(bob, dexter, 'jug')
//  @flatter
//  @templater(JSON, {
//    {
//      "name": "thunder"
//      "tribe": "COC",
//    }
//  })
//
//  @bob(1, 4, ['locksmith', "bob"])
//
//  @templater(Go, {
//  func Legover() string {
//      return "docking"
//    }
//  })
//
//  @sword(1, 4, 'bandate', "bob")
//`)

	annotations := compiler.ParseAnnotationFromReader(reader)
	assert.Len(t, annotations, 6)
	assert.Equal(t, "@terminate", annotations[0].Name)
	assert.Equal(t, "@flatter", annotations[1].Name)
	assert.Equal(t, "@templater", annotations[2].Name)
	assert.Equal(t, "@bob", annotations[3].Name)
	assert.Equal(t, "@templater", annotations[4].Name)
	assert.Equal(t, "@sword", annotations[5].Name)
}

// TestAnnotationParserWithText validates the behaviour of the comment parsing method for
// parsing comments annotations.
func TestAnnotationParserWithText(t *testing.T) {
	reader := bytes.NewBufferString(`
 The reason we wish to create this system is to allow the ability to parse such
 details outside of our scope.
  @terminate(bob, dexter, 'jug')
  @flatter
  @templater(JSON, {
    {
      "name": "thunder"
      "tribe": "COC",
    }
  })

  @bob(1, 4, ['locksmith', "bob"])

  @templater(Go, {
  func Legover() string {
      return "docking"
    }
})

  @sword(1, 4, 'bandate', "bob")
`)

	annotations := compiler.ParseAnnotationFromReader(reader)
	assert.Len(t, annotations, 6)
	assert.Equal(t, "@terminate", annotations[0].Name)
	assert.Equal(t, "@flatter", annotations[1].Name)
	assert.Equal(t, "@templater", annotations[2].Name)
	assert.Equal(t, "@bob", annotations[3].Name)
	assert.Equal(t, "@templater", annotations[4].Name)
	assert.Equal(t, "@sword", annotations[5].Name)
}

// TestAnnotationParserWithMultiComments validates the behaviour of the comment parsing method for
// parsing comments annotations.
func TestAnnotationParserWithMultiComments(t *testing.T) {
	reader := bytes.NewBufferString(`/*
* The reason we wish to create this system is to allow the ability to parse such
* details outside of our scope.
*  @terminate(bob, dexter, 'jug')
*  @flatter
*  @templater(JSON, {
*    {
*      "name": "thunder"
*      "tribe": "COC",
*    }
*  })
*
*  @bob(1, 4, ['locksmith', "bob"])
*
*  @templater(Go, {
*  func Legover() string {
*      return "docking"
*    }
*  })
*
*  @sword(1, 4, 'bandate', "bob")
*/`)

	annotations := compiler.ParseAnnotationFromReader(reader)
	assert.Len(t, annotations, 6)
	assert.Equal(t, "@terminate", annotations[0].Name)
	assert.Contains(t, annotations[0].Flags, "bob")
	assert.Contains(t, annotations[0].Flags, "dexter")
	assert.Contains(t, annotations[0].Flags, "'jug'")
	assert.Equal(t, "@flatter", annotations[1].Name)
	assert.Equal(t, "@templater", annotations[2].Name)
	assert.Equal(t, "@bob", annotations[3].Name)
	assert.Equal(t, "@templater", annotations[4].Name)
	assert.Equal(t, "@sword", annotations[5].Name)
}
