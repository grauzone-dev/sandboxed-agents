package platform

import "io"

func readPasswordLine(input io.Reader) (string, error) {
	var value []byte
	var next [1]byte
	for {
		n, err := input.Read(next[:])
		if n > 0 {
			switch next[0] {
			case '\n', '\r':
				return string(value), nil
			case 3, 4:
				return "", io.ErrUnexpectedEOF
			case 8, 127:
				if len(value) > 0 {
					value = value[:len(value)-1]
				}
			default:
				value = append(value, next[0])
				if len(value) > 65536 {
					return "", io.ErrShortBuffer
				}
			}
		}
		if err != nil {
			return "", err
		}
	}
}
