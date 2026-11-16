package seeds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// The functions here edit JSON-with-comments files in place, textually, so
// a person's comments, key order and formatting survive. Only members of a
// chosen object are touched; nested objects with the same keys are not.

type jsonMember struct {
	key      string
	keyStart int
	valStart int
	valEnd   int
}

type jsonScanner struct {
	b []byte
}

// skip moves past whitespace, // line comments and /* */ block comments.
func (s *jsonScanner) skip(i int) int {
	for i < len(s.b) {
		c := s.b[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(s.b) && s.b[i+1] == '/':
			for i < len(s.b) && s.b[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s.b) && s.b[i+1] == '*':
			end := bytes.Index(s.b[i+2:], []byte("*/"))
			if end < 0 {
				return len(s.b)
			}
			i += 2 + end + 2
		default:
			return i
		}
	}
	return i
}

func (s *jsonScanner) str(i int) (int, error) {
	if i >= len(s.b) || s.b[i] != '"' {
		return i, fmt.Errorf("expected a string at byte %d", i)
	}
	for j := i + 1; j < len(s.b); j++ {
		switch s.b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return len(s.b), errors.New("unterminated string")
}

// value returns the end of the value starting at i.
func (s *jsonScanner) value(i int) (int, error) {
	if i >= len(s.b) {
		return i, errors.New("unexpected end of input")
	}
	switch s.b[i] {
	case '"':
		return s.str(i)
	case '{':
		_, end, err := s.object(i)
		return end, err
	case '[':
		j := s.skip(i + 1)
		if j < len(s.b) && s.b[j] == ']' {
			return j + 1, nil
		}
		for {
			end, err := s.value(j)
			if err != nil {
				return end, err
			}
			j = s.skip(end)
			if j >= len(s.b) {
				return j, errors.New("unterminated array")
			}
			if s.b[j] == ']' {
				return j + 1, nil
			}
			if s.b[j] != ',' {
				return j, fmt.Errorf("expected , or ] at byte %d", j)
			}
			j = s.skip(j + 1)
		}
	}
	j := i
	for j < len(s.b) {
		c := s.b[j]
		if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' {
			break
		}
		j++
	}
	if j == i {
		return i, fmt.Errorf("unexpected %q at byte %d", s.b[i], i)
	}
	return j, nil
}

// object scans the object starting at i (which must be '{') and returns its
// members and the index just past its closing brace.
func (s *jsonScanner) object(i int) ([]jsonMember, int, error) {
	if i >= len(s.b) || s.b[i] != '{' {
		return nil, i, fmt.Errorf("expected an object at byte %d", i)
	}
	var members []jsonMember
	j := s.skip(i + 1)
	if j < len(s.b) && s.b[j] == '}' {
		return members, j + 1, nil
	}
	for {
		keyEnd, err := s.str(j)
		if err != nil {
			return nil, keyEnd, err
		}
		var key string
		if err := json.Unmarshal(s.b[j:keyEnd], &key); err != nil {
			return nil, j, err
		}
		k := s.skip(keyEnd)
		if k >= len(s.b) || s.b[k] != ':' {
			return nil, k, fmt.Errorf("expected : at byte %d", k)
		}
		vs := s.skip(k + 1)
		ve, err := s.value(vs)
		if err != nil {
			return nil, ve, err
		}
		members = append(members, jsonMember{key: key, keyStart: j, valStart: vs, valEnd: ve})
		j = s.skip(ve)
		if j >= len(s.b) {
			return nil, j, errors.New("unterminated object")
		}
		if s.b[j] == '}' {
			return members, j + 1, nil
		}
		if s.b[j] != ',' {
			return nil, j, fmt.Errorf("expected , or } at byte %d", j)
		}
		j = s.skip(j + 1)
	}
}

// rootObject returns the offset of the top-level object's opening brace.
func rootObject(b []byte) (int, error) {
	s := &jsonScanner{b: b}
	i := s.skip(0)
	if i >= len(b) || b[i] != '{' {
		return 0, errors.New("the file is not a JSON object")
	}
	if _, _, err := s.object(i); err != nil {
		return 0, err
	}
	return i, nil
}

// setMember sets key to the JSON text value in the object whose opening
// brace is at open. An existing value is replaced; a missing key is
// inserted as the first member, indented like the members around it.
func setMember(b []byte, open int, key string, value []byte) ([]byte, error) {
	s := &jsonScanner{b: b}
	members, _, err := s.object(open)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.key == key {
			return splice(b, m.valStart, m.valEnd, value), nil
		}
	}
	kb, _ := marshalText(key)
	entry := append(append(kb, ':', ' '), value...)
	if len(members) == 0 {
		return splice(b, open+1, open+1, entry), nil
	}
	first := members[0].keyStart
	gap := b[open+1 : first]
	nl := bytes.LastIndexByte(gap, '\n')
	if nl < 0 {
		// Members on the brace's line: insert inline before the first.
		return splice(b, first, first, append(entry, ',', ' ')), nil
	}
	// The indentation of the first member's line, and the file's line ending.
	lineStart := bytes.LastIndexByte(b[:first], '\n') + 1
	indent := b[lineStart:first]
	for k, c := range indent {
		if c != ' ' && c != '\t' {
			indent = indent[:k]
			break
		}
	}
	eol := []byte("\n")
	if bytes.Contains(b, []byte("\r\n")) {
		eol = []byte("\r\n")
	}
	ins := append(append(append(append([]byte{}, eol...), indent...), entry...), ',')
	return splice(b, open+1, open+1, ins), nil
}

// memberValue returns the raw text of key in the object at open.
func memberValue(b []byte, open int, key string) (jsonMember, bool, error) {
	s := &jsonScanner{b: b}
	members, _, err := s.object(open)
	if err != nil {
		return jsonMember{}, false, err
	}
	for _, m := range members {
		if m.key == key {
			return m, true, nil
		}
	}
	return jsonMember{}, false, nil
}

func splice(b []byte, from, to int, ins []byte) []byte {
	out := make([]byte, 0, len(b)-(to-from)+len(ins))
	out = append(out, b[:from]...)
	out = append(out, ins...)
	return append(out, b[to:]...)
}

// marshalText encodes v as JSON without HTML escaping.
func marshalText(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SetTopLevel sets key to v among the top-level members of a JSON (with
// comments) document, keeping everything else byte for byte.
func SetTopLevel(b []byte, key string, v any) ([]byte, error) {
	open, err := rootObject(b)
	if err != nil {
		return nil, err
	}
	val, err := marshalText(v)
	if err != nil {
		return nil, err
	}
	return setMember(b, open, key, val)
}

// SetPinText points the top-level "pin" object of an environment.json text
// at persona and version. The version is replaced or inserted; the persona
// is replaced or inserted only when it differs. A missing or non-object pin
// is replaced by a new object.
func SetPinText(b []byte, persona string, version int) ([]byte, error) {
	open, err := rootObject(b)
	if err != nil {
		return nil, err
	}
	m, ok, err := memberValue(b, open, "pin")
	if err != nil {
		return nil, err
	}
	if !ok || b[m.valStart] != '{' {
		pb, _ := marshalText(persona)
		return setMember(b, open, "pin", []byte(fmt.Sprintf(`{ "persona": %s, "version": %d }`, pb, version)))
	}
	pinOpen := m.valStart
	vb, _ := marshalText(version)
	if b, err = setMember(b, pinOpen, "version", vb); err != nil {
		return nil, err
	}
	pm, ok, err := memberValue(b, pinOpen, "persona")
	if err != nil {
		return nil, err
	}
	var cur string
	if ok {
		json.Unmarshal(b[pm.valStart:pm.valEnd], &cur)
	}
	if !ok || cur != persona {
		pb, _ := marshalText(persona)
		if b, err = setMember(b, pinOpen, "persona", pb); err != nil {
			return nil, err
		}
	}
	return b, nil
}
