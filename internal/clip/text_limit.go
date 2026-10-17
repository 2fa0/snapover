package clip

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// TextTooLarge reports a clipboard or history text that exceeds the active limit.
func TextTooLarge(n, limit int) error {
	if limit <= 0 || limit > MaxTextBytes {
		limit = MaxTextBytes
	}
	if n < limit {
		n = limit + 1
	}
	return fmt.Errorf("文本 %d 字节，超过 %d MB 上限，可在设置中调整文本大小上限", n, limit>>20)
}

func normalizeTextLimit(limit int) int {
	if limit <= 0 || limit > MaxTextBytes {
		return MaxTextBytes
	}
	return limit
}

// decodeUTF16Limited transcodes NUL-terminated UTF-16 and stops before the
// output exceeds limit. The input itself must already be bounded by the caller.
func decodeUTF16Limited(raw []byte, limit int) (string, error) {
	limit = normalizeTextLimit(limit)
	if len(raw) < 2 {
		return "", nil
	}
	units := len(raw) / 2
	out := make([]byte, 0, min(limit, units*3))
	for i := 0; i+1 < len(raw); {
		u := uint16(raw[i]) | uint16(raw[i+1])<<8
		if u == 0 {
			break
		}
		var r rune
		var n int
		if utf16.IsSurrogate(rune(u)) {
			if i+3 >= len(raw) {
				break
			}
			pair := []uint16{u, uint16(raw[i+2]) | uint16(raw[i+3])<<8}
			r = utf16.Decode(pair)[0]
			n = 4
		} else {
			r = rune(u)
			n = 2
		}
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 3
			r = utf8.RuneError
		}
		if len(out)+size > limit {
			return "", TextTooLarge(len(out)+size, limit)
		}
		out = utf8.AppendRune(out, r)
		i += n
	}
	return string(out), nil
}
