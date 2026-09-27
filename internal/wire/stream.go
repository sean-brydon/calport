package wire

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// streamConn presents one HTTP/2 stream to a box port as a net.Conn, so it can
// back a plain TCP forward or an HTTP reverse proxy.
type streamConn struct {
	body   io.ReadCloser
	pw     *io.PipeWriter
	cancel context.CancelFunc
	remote net.Addr
	once   sync.Once
}

func (s *streamConn) Read(b []byte) (int, error)  { return s.body.Read(b) }
func (s *streamConn) Write(b []byte) (int, error) { return s.pw.Write(b) }

// CloseWrite half-closes the stream: the box's upstream connection sees EOF
// while replies can still arrive.
func (s *streamConn) CloseWrite() error { return s.pw.Close() }

func (s *streamConn) Close() error {
	s.once.Do(func() {
		s.pw.Close()
		s.body.Close()
		s.cancel()
	})
	return nil
}

func (s *streamConn) LocalAddr() net.Addr  { return streamAddr("calport") }
func (s *streamConn) RemoteAddr() net.Addr { return s.remote }

// Deadlines are not supported: a stream ends through Close, and every caller
// in calport bounds its work with a context instead.
func (s *streamConn) SetDeadline(time.Time) error      { return nil }
func (s *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (s *streamConn) SetWriteDeadline(time.Time) error { return nil }

type streamAddr string

func (a streamAddr) Network() string { return "calport" }
func (a streamAddr) String() string  { return string(a) }
