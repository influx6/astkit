package compiler

import "go/token"

import "gitlab.com/gokit/astkit/internal/ioutil"

// ReadSource returns giving source lines and token.Position for giving target.
func ReadSource(t *token.FileSet, begin token.Pos, end token.Pos) ([]byte, token.Position, token.Position, error) {
	length, beginPosition, endPosition := GetPosition(t, begin, end)
	sourceLines, err := ioutil.ReadBytesFrom(beginPosition.Filename, int64(beginPosition.Offset), length)
	return sourceLines, beginPosition, endPosition, err
}

// GetPosition returns giving source lines length and token.Positions for giving target begin and end data.
func GetPosition(t *token.FileSet, begin token.Pos, end token.Pos) (int, token.Position, token.Position) {
	beginPosition := t.Position(begin)
	endPosition := t.Position(end)
	length := endPosition.Offset - beginPosition.Offset
	return length, beginPosition, endPosition
}
