package terminal

import (
	"encoding/binary"
	"errors"
	"io"
)

// Input from the laptop to an attached session is framed so window resizes
// can travel alongside keystrokes: [type][length uint16][payload]. Output
// from the session is sent as plain bytes.
const (
	FrameData   byte = 0
	FrameResize byte = 1

	maxFrame = 32 << 10
)

func WriteData(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n := min(len(b), maxFrame)
		if err := writeFrame(w, FrameData, b[:n]); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func WriteResize(w io.Writer, cols, rows int) error {
	var p [4]byte
	binary.BigEndian.PutUint16(p[0:], uint16(cols))
	binary.BigEndian.PutUint16(p[2:], uint16(rows))
	return writeFrame(w, FrameResize, p[:])
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	buf := make([]byte, 3+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint16(buf[1:], uint16(len(payload)))
	copy(buf[3:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrames delivers frames from r until it ends. A malformed frame ends
// the stream rather than being guessed at.
func ReadFrames(r io.Reader, data func([]byte) error, resize func(cols, rows int)) error {
	var head [3]byte
	buf := make([]byte, maxFrame)
	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		n := int(binary.BigEndian.Uint16(head[1:]))
		if n > maxFrame {
			return errors.New("terminal frame too large")
		}
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return err
		}
		switch head[0] {
		case FrameData:
			if err := data(buf[:n]); err != nil {
				return err
			}
		case FrameResize:
			if n != 4 {
				return errors.New("malformed resize frame")
			}
			resize(int(binary.BigEndian.Uint16(buf[0:])), int(binary.BigEndian.Uint16(buf[2:])))
		default:
			return errors.New("unknown terminal frame")
		}
	}
}
