package compiler

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strings"
)

// ParseAnnotationFromReader implements a short parser to transform annotation style text (i.e commands prefixed with @)
// into Annotation structures useful for indicating processing directives.
/*
 Annotations are of the format:

	 1. @flatter
	 2. @terminate(bob, dexter, 'jug')
	 3. @templater(JSON, {
	   {
		 "name": "thunder"
		 "tribe": "COC",
	   }
	 })

	 4. @bob(1, 4, ['locksmith', "bob"])

	 5. @templater(Go, {
		 func Legover() string {
			 return "docking"
		}
	 })

	 6. @sword(1, 4, 'bandate', "bob")

*/
func ParseAnnotationFromReader(r io.Reader) []Annotation {
	var annotations []Annotation

	reader := bufio.NewReader(r)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		trimmedline := string(cleanWord([]byte(line)))
		if trimmedline == "" {
			continue
		}

		// Do we have a annotation here?
		if !strings.HasPrefix(trimmedline, "@") {
			continue
		}

		params := make(map[string]string, 0)

		if !strings.Contains(trimmedline, "(") {
			annotations = append(annotations, Annotation{Name: trimmedline, Params: params})
			continue
		}

		argIndex := strings.IndexRune(trimmedline, '(')
		argName := trimmedline[:argIndex]
		argContents := trimmedline[argIndex:]

		// Do we have a template associated with this annotation, if not, split the
		// commas and let those be our arguments.
		if !strings.HasSuffix(argContents, "{") {

			argContents = strings.TrimPrefix(strings.TrimSuffix(argContents, ")"), "(")

			var parts []string

			for _, part := range strings.Split(argContents, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed == "" {
					continue
				}

				parts = append(parts, trimmed)

				// If we are dealing with key value pairs then split, trimspace and set
				// in params. We only expect 2 values, any more and we wont consider the rest.
				if kvPieces := strings.Split(trimmed, "=>"); len(kvPieces) > 1 {
					val := strings.TrimSpace(kvPieces[1])
					params[strings.TrimSpace(kvPieces[0])] = val
				}
			}

			annotations = append(annotations, Annotation{
				Flags:  parts,
				Name:   argName,
				Params: params,
			})

			continue
		}

		templateIndex := strings.IndexRune(argContents, '{')
		templateArgs := argContents[:templateIndex]
		templateArgs = strings.TrimPrefix(strings.TrimSuffix(templateArgs, ")"), "(")

		var parts []string

		for _, part := range strings.Split(templateArgs, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}

			parts = append(parts, trimmed)

			// If we are dealing with key value pairs then split, trimspace and set
			// in params. We only expect 2 values, any more and we wont consider the rest.
			if kvPieces := strings.Split(trimmed, "=>"); len(kvPieces) > 1 {
				val := strings.TrimSpace(kvPieces[1])
				params[strings.TrimSpace(kvPieces[0])] = val
			}
		}

		template := strings.TrimSpace(readTemplate(reader))
		annotations = append(annotations, Annotation{
			Flags:    parts,
			Name:     argName,
			Template: template,
			Params:   params,
		})

	}

	return annotations
}

var (
	ending           = []byte("})")
	newline          = []byte("\n")
	empty            = []byte("")
	singleComment    = []byte("//")
	multiComment     = []byte("/*")
	multiCommentItem = []byte("*")
	commentry        = regexp.MustCompile(`\s*?([\/\/*|\*|\/]+)`)
)

func readTemplate(reader *bufio.Reader) string {
	var bu bytes.Buffer

	var seenEnd bool

	for {
		// Do we have another pending prefix, if so, we are at the ending, so return.
		if seenEnd {
			data, _ := reader.Peek(100)
			dataVal := commentry.ReplaceAllString(string(data), "")
			dataVal = string(cleanWord([]byte(dataVal)))

			if strings.HasPrefix(string(dataVal), "@") {
				return bu.String()
			}

			// If it's all space, then return.
			// if strings.TrimSpace(string(dataVal)) == "" {
			return bu.String()
			// }
		}

		twoWord, err := reader.ReadString('\n')
		if err != nil {
			bu.WriteString(twoWord)
			return bu.String()
		}

		twoWorded := cleanWord([]byte(twoWord))
		// fmt.Printf("Ending: %+q -> %t\n", twoWorded, bytes.HasPrefix(twoWorded, ending))

		if bytes.HasPrefix(twoWorded, ending) {
			seenEnd = true
			continue
		}

		bu.WriteString(twoWord)
	}

}

func cleanWord(word []byte) []byte {
	word = bytes.TrimSpace(word)
	word = bytes.TrimPrefix(word, singleComment)
	word = bytes.TrimPrefix(word, multiComment)
	word = bytes.TrimPrefix(word, multiCommentItem)
	return bytes.TrimSpace(word)
}
