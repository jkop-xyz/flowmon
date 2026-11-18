package ipfix

import (
	"encoding/binary"
	"errors"
)

// errShort is returned when a read would go past the end of the buffer.
var errShort = errors.New("ipfix: not enough bytes")

// cursor reads big-endian values from a byte slice. Every read is bounds-checked
// and returns an error instead of panicking; nothing is ever allocated from a length
// that hasn't been checked against the remaining bytes first.
type cursor struct {
	buf []byte
	off int
}

func newCursor(b []byte) *cursor { return &cursor{buf: b} }

func (c *cursor) remaining() int { return len(c.buf) - c.off }

func (c *cursor) u8() (uint8, error) {
	if c.remaining() < 1 {
		return 0, errShort
	}
	v := c.buf[c.off]
	c.off++
	return v, nil
}

func (c *cursor) u16() (uint16, error) {
	if c.remaining() < 2 {
		return 0, errShort
	}
	v := binary.BigEndian.Uint16(c.buf[c.off:])
	c.off += 2
	return v, nil
}

func (c *cursor) u32() (uint32, error) {
	if c.remaining() < 4 {
		return 0, errShort
	}
	v := binary.BigEndian.Uint32(c.buf[c.off:])
	c.off += 4
	return v, nil
}

// bytes returns the next n bytes without copying. n is checked before slicing.
func (c *cursor) bytes(n int) ([]byte, error) {
	if n < 0 || c.remaining() < n {
		return nil, errShort
	}
	b := c.buf[c.off : c.off+n : c.off+n]
	c.off += n
	return b, nil
}

// rest returns everything not read yet.
func (c *cursor) rest() []byte {
	b := c.buf[c.off:len(c.buf):len(c.buf)]
	c.off = len(c.buf)
	return b
}
