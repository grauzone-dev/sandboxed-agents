package platform

import "io"

// MaxPasswordBytes is the longest secret, in bytes, that a hidden prompt or piped input accepts.
const MaxPasswordBytes = 65536

func readPasswordLine(input io.Reader) (string, error) {
	const (
		endOfText         = 3
		endOfTransmission = 4
		backspace         = 8
		deleteCharacter   = 127
	)
	var value []byte
	var next [1]byte
	for {
		n, err := input.Read(next[:])
		if n > 0 {
			switch next[0] {
			case '\n', '\r':
				return string(value), nil
			case endOfText, endOfTransmission:
				return "", io.ErrUnexpectedEOF
			case backspace, deleteCharacter:
				if len(value) > 0 {
					value = value[:len(value)-1]
				}
			default:
				value = append(value, next[0])
				if len(value) > MaxPasswordBytes {
					return "", io.ErrShortBuffer
				}
			}
		}
		if err != nil {
			return "", err
		}
	}
}
