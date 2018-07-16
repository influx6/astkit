package ioutil

import (
	"os"
)

// ReadBytesFrom returns giving data at giving offset of provided length else
// returning an error.
func ReadBytesFrom(path string, offset int64, length int) ([]byte, error) {
	inFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer inFile.Close()

	inFile.Seek(offset, 0)

	content := make([]byte, length)
	_, err = inFile.Read(content)
	return content, err
}
