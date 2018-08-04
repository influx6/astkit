package compiler

import "go/token"

import "gitlab.com/gokit/astkit/internal/ioutil"

// ReadSource returns giving source lines and token.Position for giving target.
// If an error occurred in attempt to load the source, the error is returned and
// the position data as well.
func ReadSource(t *token.FileSet, begin token.Pos, end token.Pos) ([]byte, int, token.Position, token.Position, error) {
	length, beginPosition, endPosition := GetPosition(t, begin, end)
	sourceLines, err := ioutil.ReadBytesFrom(beginPosition.Filename, int64(beginPosition.Offset), length)
	return sourceLines, length, beginPosition, endPosition, err
}

// ReadSourceIfPossible will call ReadSource() and return the data if receives excluding the error. Because
// there is a possibility that the position data is invalid and hence file data is invalid, an error can occur,
// this method will just return such data to you without error that occurred.
func ReadSourceIfPossible(t *token.FileSet, begin token.Pos, end token.Pos) ([]byte, int, token.Position, token.Position) {
	source, length, beginPosition, endPosition, _ := ReadSource(t, begin, end)
	return source, length, beginPosition, endPosition
}

// GetPosition returns giving source lines length and token.Positions for giving target begin and end data.
func GetPosition(t *token.FileSet, begin token.Pos, end token.Pos) (int, token.Position, token.Position) {
	beginPosition := t.Position(begin)
	endPosition := t.Position(end)
	length := endPosition.Offset - beginPosition.Offset
	return length, beginPosition, endPosition
}
