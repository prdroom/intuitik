// Package server is the local server of intuitik: one listener on 127.0.0.1, a
// one-time login token, per-tab sessions and the WebSocket (packet M1-T2).
package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// tokenLifetime is V3; the no-login notice V4 fires at the same moment.
const tokenLifetime = 60 * time.Second

// Options are the inputs of Run.
type Options struct {
	Assets        fs.FS     // the embedded web build
	Stdin         io.Reader // read for Enter when StdinTerminal
	StdinTerminal bool
	Stdout        io.Writer
	Stderr        io.Writer
	Browser       string           // the BROWSER environment variable (item P1 (a))
	Stop          <-chan os.Signal // the normal stop (interrupt)
	Seams         Seams
}

type token struct {
	value    string
	issued   time.Time
	used     bool // exchanged
	replaced bool // made invalid by Enter while unused
	notified bool // the no-login line was printed
}

// state is the run state of S19. Every field is guarded by mu.
type state struct {
	mu            sync.Mutex
	tokens        []*token
	sessions      []string
	csrf          string // C, one per run (S6)
	sessionIssued bool   // a session was issued in this run (S8 (iii))
	conns         map[*websocket.Conn]struct{}
	stopping      bool
}

type server struct {
	o        Options
	port     int
	origin   string // http://127.0.0.1:P
	host     string // 127.0.0.1:P
	lifetime time.Duration
	static   map[string]staticFile
	routes   []Route // the registry: every route registered by controlRoute or clusterRoute
	st       state
	cluster  cluster    // the connection state of the active context (packet M1-T3a)
	outMu    sync.Mutex // one line at a time on stdout and stderr
}

// newSecret is 256 bits from crypto/rand, base64url without padding (section 6.1).
func newSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// equal compares two secrets in constant time (S4).
func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *server) now() time.Time {
	if s.o.Seams.Now != nil {
		return s.o.Seams.Now()
	}
	return time.Now()
}

func (s *server) println(w io.Writer, line string) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintln(w, line)
}

// Run serves until the normal stop (exit code 0) or until the listener is lost
// (exit code 1). It returns 1 when no listener can be opened.
func Run(o Options) int {
	s := &server{o: o, lifetime: tokenLifetime}
	s.registerRoutes()
	s.cluster.print = s.printOutcome
	if o.Seams.Fixture.TokenLifetime != 0 {
		s.lifetime = o.Seams.Fixture.TokenLifetime
	}
	static, err := loadStatic(o.Assets)
	if err != nil {
		fmt.Fprintln(o.Stderr, "intuitik:", err)
		return 1
	}
	s.static = static
	s.st.csrf = newSecret()
	s.st.conns = map[*websocket.Conn]struct{}{}

	ln, failure := ListenLoopback(o.Seams)
	if ln == nil {
		fmt.Fprintln(o.Stderr, failure)
		return 1
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.host = fmt.Sprintf("%s:%d", listenAddr, s.port)
	s.origin = "http://" + s.host

	var protocols http.Protocols
	protocols.SetHTTP1(true) // HTTP/1.1 only, no h2c (S13)
	srv := &http.Server{
		Handler:                      s.handler(),
		ReadHeaderTimeout:            10 * time.Second,
		Protocols:                    &protocols,
		DisableGeneralOptionsHandler: true,
		ErrorLog:                     log.New(io.Discard, "", 0),
	}

	s.println(o.Stdout, fmt.Sprintf(textListen, s.port))
	if o.StdinTerminal {
		s.println(o.Stdout, TextTerminal)
	} else {
		s.println(o.Stdout, TextNotTerminal)
	}
	// The first connection attempt starts after the listen line (W2).
	s.cluster.start("")

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	s.open(s.issue(), false)
	if o.StdinTerminal {
		go s.readEnter()
	}
	watchDone := make(chan struct{})
	defer close(watchDone)
	go s.watchNoLogin(watchDone)

	select {
	case <-o.Stop:
		s.stop()
		releaseListener(ln)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		return 0
	case err := <-served:
		// The listener closed while running (S1): every token and session goes with
		// the process.
		s.st.mu.Lock()
		s.st.tokens, s.st.sessions = nil, nil
		s.st.mu.Unlock()
		s.println(o.Stderr, fmt.Sprintf("intuitik: the listener on %s closed: %s", s.host, osError(err)))
		return 1
	}
}

// issue makes a new one-time token. Unused earlier tokens become invalid (S18).
func (s *server) issue() string {
	t := &token{value: newSecret(), issued: s.now()}
	s.st.mu.Lock()
	for _, old := range s.st.tokens {
		if !old.used {
			old.replaced = true
		}
	}
	s.st.tokens = append(s.st.tokens, t)
	s.st.mu.Unlock()
	return t.value
}

func (s *server) loginURL(t string) string {
	return s.origin + "/#t=" + t
}

// open passes the login address to the opener as one argument (S18). With BROWSER
// set, its whole value is one executable started without a shell; otherwise the OS
// default opener. On failure it prints S-O and never a query address. After Enter it
// prints S-R.
func (s *server) open(t string, enter bool) {
	url := s.loginURL(t)
	if enter {
		s.println(s.o.Stderr, TextNewAddress+url)
	}
	go func() {
		if err := s.startOpener(url); err != nil {
			s.println(s.o.Stderr, TextOpenFailed+url)
		}
	}()
}

func (s *server) startOpener(url string) error {
	if s.o.Browser != "" {
		// Run waits for the executable: one that starts and then exits unsuccessfully
		// is an open failure too (S-O). open calls this in its own goroutine.
		return exec.Command(s.o.Browser, url).Run()
	}
	if s.o.Seams.OpenDefault != nil {
		return s.o.Seams.OpenDefault(url)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Run()
}

// readEnter makes a new one-time address for each line on the terminal (S19).
func (s *server) readEnter() {
	r := bufio.NewReader(s.o.Stdin)
	for {
		if _, err := r.ReadString('\n'); err != nil {
			return
		}
		t := s.issue()
		if f := s.o.Seams.Fixture.OnEnter; f != nil {
			f(&s.st)
		}
		s.open(t, true)
	}
}

// watchNoLogin prints the no-login line once for each token that was neither
// exchanged nor replaced when its lifetime ended (V4).
func (s *server) watchNoLogin(done <-chan struct{}) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		now := s.now()
		fire := 0
		s.st.mu.Lock()
		for _, t := range s.st.tokens {
			if !t.used && !t.replaced && !t.notified && now.Sub(t.issued) >= tokenLifetime {
				t.notified = true
				fire++
			}
		}
		s.st.mu.Unlock()
		for ; fire > 0; fire-- {
			if s.o.StdinTerminal {
				s.println(s.o.Stderr, TextNoLoginTerminal)
			} else {
				s.println(s.o.Stderr, TextNoLoginNotTerminal)
			}
		}
	}
}

// exchangeResult is the outcome of one exchange attempt (S3, S12).
type exchangeResult int

const (
	exchanged exchangeResult = iota
	refusedUnknown
	refusedUsed // consumed, or made invalid by Enter: S-W
	refusedExpired
)

// exchange consumes t once and issues a new session. Concurrent attempts are
// serialized by the state lock, so exactly one of them can succeed.
func (s *server) exchange(t string) (exchangeResult, string, string) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	var match *token
	for _, tk := range s.st.tokens {
		if equal(tk.value, t) {
			match = tk
		}
	}
	switch {
	case match == nil:
		return refusedUnknown, "", ""
	case match.used || match.replaced:
		return refusedUsed, "", ""
	case s.now().Sub(match.issued) >= s.lifetime:
		return refusedExpired, "", ""
	}
	match.used = true
	sess := newSecret()
	s.st.sessions = append(s.st.sessions, sess)
	s.st.sessionIssued = true
	return exchanged, sess, s.st.csrf
}

// validSession compares against every session without stopping early (S4).
func (s *server) validSession(sess string) bool {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	ok := false
	for _, v := range s.st.sessions {
		if equal(v, sess) {
			ok = true
		}
	}
	return ok
}

func (s *server) csrf() string {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	return s.st.csrf
}

func (s *server) sessionIssued() bool {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	return s.st.sessionIssued
}

// stop sends the stop notice on every open WebSocket, then closes it (S8 (ii)).
func (s *server) stop() {
	s.st.mu.Lock()
	s.st.stopping = true
	conns := make([]*websocket.Conn, 0, len(s.st.conns))
	for c := range s.st.conns {
		conns = append(conns, c)
	}
	s.st.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Write(ctx, websocket.MessageText, []byte(stopNotice))
			c.Close(websocket.StatusGoingAway, "")
		}()
	}
	wg.Wait()
}
