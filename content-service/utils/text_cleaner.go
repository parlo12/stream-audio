package utils

import (
	"bytes"
	"io/ioutil"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func CleanUTF8(input []byte) string {
	reader := transform.NewReader(bytes.NewReader(input), unicode.UTF8.NewDecoder())
	result, _ := ioutil.ReadAll(reader)
	return string(result)
}
