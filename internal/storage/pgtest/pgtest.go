// Package pgtest holds the PostgreSQL test helpers the storage tests share: a socket that refuses
// connections, and a proxy that refuses them until it starts.
package pgtest

import (
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// socketName is the file pgx dials in a socket directory for port 5432.
const socketName = ".s.PGSQL.5432"

// RefusingDir returns a directory holding a Unix socket nobody listens on, so a dial there is refused
// by the kernel. A released TCP port can be taken by another process before the dial; this path is the
// test's own.
func RefusingDir(t testing.TB) string {
	t.Helper()
	// Not t.TempDir: it names the directory after the test, past the 104 bytes macOS allows a socket path.
	dir, err := os.MkdirTemp("", "pg")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, os.RemoveAll(dir)) })
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, socketName), Net: "unix"})
	require.NoError(t, err)
	// Without the file the dial fails with "no such file", not a refusal.
	l.SetUnlinkOnClose(false)
	require.NoError(t, l.Close())
	return dir
}

// DSN points dsn at the socket in dir. The port is set so a PGPORT in the environment cannot change
// the socket's file name, and TLS stays off, so one attempt is one connection.
func DSN(t testing.TB, dsn, dir string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Host = ""
	q := u.Query()
	q.Set("host", dir)
	q.Set("port", "5432")
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

// Proxy forwards a Unix socket to the real database once started. Until then the socket refuses, so
// the driver sees a real refused connection without the database being stopped.
type Proxy struct {
	t           testing.TB
	dir         string
	target      string
	connections atomic.Int32

	mu       sync.Mutex
	closed   bool
	listener net.Listener
	conns    []net.Conn
}

// NewProxy returns a proxy to target, a host:port, that refuses until Start. The test's cleanup
// closes it.
func NewProxy(t testing.TB, target string) *Proxy {
	t.Helper()
	p := &Proxy{t: t, dir: RefusingDir(t), target: target}
	t.Cleanup(p.close)
	return p
}

// DSN returns dsn pointed at the proxy.
func (p *Proxy) DSN(dsn string) string {
	p.t.Helper()
	return DSN(p.t, dsn, p.dir)
}

// Connections counts the connections accepted since Start.
func (p *Proxy) Connections() int {
	return int(p.connections.Load())
}

func (p *Proxy) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	path := filepath.Join(p.dir, socketName)
	// A listen on a path that exists fails, and the refusing socket's file is still there.
	if !assert.NoError(p.t, os.Remove(path)) {
		return
	}
	l, err := net.Listen("unix", path)
	if !assert.NoError(p.t, err) {
		return
	}
	p.listener = l
	go p.serve(l)
}

func (p *Proxy) StartAfter(d time.Duration) {
	timer := time.AfterFunc(d, p.Start)
	p.t.Cleanup(func() { timer.Stop() })
}

func (p *Proxy) serve(l net.Listener) {
	for {
		client, err := l.Accept()
		if err != nil {
			return
		}
		p.connections.Add(1)
		server, err := net.Dial("tcp", p.target)
		if !assert.NoError(p.t, err) {
			client.Close()
			return
		}
		p.mu.Lock()
		p.conns = append(p.conns, client, server)
		p.mu.Unlock()
		go forward(client, server)
		go forward(server, client)
	}
}

func forward(to, from net.Conn) {
	io.Copy(to, from)
	to.Close()
	from.Close()
}

func (p *Proxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.listener != nil {
		p.listener.Close()
	}
	for _, c := range p.conns {
		c.Close()
	}
}
