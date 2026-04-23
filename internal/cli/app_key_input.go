package cli

import "unicode/utf8"

type rawKeyPoller interface {
	Poll() ([]rawKey, error)
	Close() error
}

type rawKeyDecoder struct {
	buf []byte
}

func (d *rawKeyDecoder) Append(data []byte) []rawKey {
	d.buf = append(d.buf, data...)
	var keys []rawKey
	for len(d.buf) > 0 {
		key, consumed, complete := decodeRawKeyPrefix(d.buf)
		if !complete {
			break
		}
		d.buf = d.buf[consumed:]
		if key.kind != rawKeyNone {
			keys = append(keys, key)
		}
	}
	return keys
}

func decodeRawKeyPrefix(buf []byte) (rawKey, int, bool) {
	if len(buf) == 0 {
		return rawKey{}, 0, false
	}
	b := buf[0]
	switch b {
	case '\r', '\n':
		return rawKey{kind: rawKeyEnter}, 1, true
	case 0x01:
		return rawKey{kind: rawKeyHome}, 1, true
	case 0x03:
		return rawKey{kind: rawKeyCtrlC}, 1, true
	case 0x04:
		return rawKey{kind: rawKeyCtrlD}, 1, true
	case 0x05:
		return rawKey{kind: rawKeyEnd}, 1, true
	case 0x0c:
		return rawKey{kind: rawKeyClear}, 1, true
	case 0x7f, 0x08:
		return rawKey{kind: rawKeyBackspace}, 1, true
	case 0x1b:
		return decodeEscapeKeyPrefix(buf)
	}
	if b < 0x20 {
		return rawKey{kind: rawKeyNone}, 1, true
	}
	if b < utf8.RuneSelf {
		return rawKey{kind: rawKeyRune, char: rune(b)}, 1, true
	}
	if !utf8.FullRune(buf) {
		return rawKey{}, 0, false
	}
	rr, size := utf8.DecodeRune(buf)
	if rr == utf8.RuneError && size == 1 {
		return rawKey{kind: rawKeyNone}, 1, true
	}
	return rawKey{kind: rawKeyRune, char: rr}, size, true
}

func decodeEscapeKeyPrefix(buf []byte) (rawKey, int, bool) {
	if len(buf) < 2 {
		return rawKey{}, 0, false
	}
	switch buf[1] {
	case '[':
		return decodeCSIKeyPrefix(buf)
	case 'O':
		if len(buf) < 3 {
			return rawKey{}, 0, false
		}
		return parseSS3Key(buf[2]), 3, true
	default:
		return rawKey{kind: rawKeyNone}, 2, true
	}
}

func decodeCSIKeyPrefix(buf []byte) (rawKey, int, bool) {
	const maxCSIBytes = 64
	for index := 2; index < len(buf) && index < maxCSIBytes+2; index++ {
		if isCSIFinal(buf[index]) {
			return parseCSIKey(buf[2:index], buf[index]), index + 1, true
		}
	}
	if len(buf) >= maxCSIBytes+2 {
		return rawKey{kind: rawKeyNone}, maxCSIBytes + 2, true
	}
	return rawKey{}, 0, false
}

func parseSS3Key(code byte) rawKey {
	switch code {
	case 'A':
		return rawKey{kind: rawKeyHistoryPrev}
	case 'B':
		return rawKey{kind: rawKeyHistoryNext}
	case 'C':
		return rawKey{kind: rawKeyRight}
	case 'D':
		return rawKey{kind: rawKeyLeft}
	case 'F':
		return rawKey{kind: rawKeyEnd}
	case 'H':
		return rawKey{kind: rawKeyHome}
	}
	return rawKey{kind: rawKeyNone}
}
