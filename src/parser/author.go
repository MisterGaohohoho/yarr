package parser

import (
	"encoding/xml"
	"strings"
)

// xmlAuthor accepts both text authors and structured RSS/Atom authors.
type xmlAuthor string

func (a *xmlAuthor) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	var text, name strings.Builder
	depth, nameDepth := 1, 0
	selected := ""
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if token.Name.Local == "name" && nameDepth == 0 && selected == "" {
				nameDepth = depth
				name.Reset()
			}
		case xml.CharData:
			text.Write(token)
			if nameDepth > 0 {
				name.Write(token)
			}
		case xml.EndElement:
			if depth == nameDepth {
				selected = strings.Join(strings.Fields(name.String()), " ")
				nameDepth = 0
			}
			depth--
		}
	}
	if selected == "" {
		selected = strings.Join(strings.Fields(text.String()), " ")
	}
	*a = xmlAuthor(selected)
	return nil
}

func firstAuthor(groups ...[]xmlAuthor) string {
	for _, authors := range groups {
		for _, author := range authors {
			if name := strings.TrimSpace(string(author)); name != "" {
				return name
			}
		}
	}
	return ""
}
