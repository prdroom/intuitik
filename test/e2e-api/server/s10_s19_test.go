//go:build e2e && linux

package server

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

func countSecret(b []byte, s string) int { return secretscan.Count(b, s) }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// stderrCheck is the check of packet section 6.1: only S-O and S-R lines may hold a
// token, and only the T or T' inside them; every other occurrence of a token, and
// every S and C, is 0. It also covers stdout.
func stderrCheck(stdout, stderr []byte, tokens, others []string) []string {
	var problems []string
	allowed := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(stderr), "\r\n", "\n"), "\n") {
		for _, prefix := range []string{textSO, textSR} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				if m := loginURL.FindStringSubmatch(rest); m != nil {
					allowed[m[2]] = true
				}
			}
		}
	}
	for _, tok := range tokens {
		inLines := 0
		var rest []string
		for _, line := range strings.Split(strings.ReplaceAll(string(stderr), "\r\n", "\n"), "\n") {
			if (strings.HasPrefix(line, textSO) || strings.HasPrefix(line, textSR)) && strings.HasSuffix(line, "/#t="+tok) {
				inLines++
				continue
			}
			rest = append(rest, line)
		}
		if n := countSecret([]byte(strings.Join(rest, "\n")), tok); n != 0 {
			problems = append(problems, fmt.Sprintf("a token appears %d time(s) in stderr outside S-O and S-R", n))
		}
		if n := countSecret(stdout, tok); n != 0 {
			problems = append(problems, fmt.Sprintf("a token appears %d time(s) in stdout", n))
		}
		_ = inLines
	}
	for _, s := range others {
		if n := countSecret(stderr, s) + countSecret(stdout, s); n != 0 {
			problems = append(problems, fmt.Sprintf("a session or CSRF value appears %d time(s) in stdout or stderr", n))
		}
	}
	return problems
}

// S10: the searcher finds a planted canary in every searched form.
func TestS10_SearcherPositiveControl(t *testing.T) {
	canary := randomToken()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("body " + canary + " end"))
	zw.Close()
	var fl bytes.Buffer
	fw, _ := flate.NewWriter(&fl, flate.BestCompression)
	fw.Write([]byte(`{"session":"` + canary + `"}`))
	fw.Flush()
	deflated := bytes.TrimSuffix(fl.Bytes(), []byte{0x00, 0x00, 0xff, 0xff})
	cases := map[string][]byte{
		"raw":                []byte("x " + canary + " y"),
		"base64url":          []byte(base64.RawURLEncoding.EncodeToString([]byte(canary))),
		"base64 shift 0":     []byte(base64.StdEncoding.EncodeToString([]byte(canary))),
		"base64 shift 1":     []byte(base64.StdEncoding.EncodeToString([]byte("a" + canary))),
		"base64 shift 2":     []byte(base64.StdEncoding.EncodeToString([]byte("ab" + canary))),
		"gzip":               gz.Bytes(),
		"permessage-deflate": deflated,
	}
	for name, data := range cases {
		n := secretscan.Count(data, canary)
		t.Logf("S10 positive control %s: found %d", name, n)
		if n < 1 {
			t.Errorf("the searcher missed the canary in form %s", name)
		}
	}
	if n := secretscan.Count([]byte("nothing here"), canary); n != 0 {
		t.Errorf("the searcher found %d in clean data", n)
	}
	// In a real run: S-O prints T; the searcher finds it in that stderr.
	p := startPanel(t, panelOpts{browser: strPtr("/nonexistent/opener")})
	line := p.stderr.wait(t, "S-O", 10*time.Second, func(s string) bool { return strings.HasPrefix(s, textSO) })
	tok := loginURL.FindStringSubmatch(strings.TrimPrefix(line, textSO))[2]
	n := secretscan.Count(p.stderr.bytes(), tok)
	t.Logf("S10 positive control in a real run: T found %d time(s) in stderr (the S-O line)", n)
	if n < 1 {
		t.Error("the searcher missed T in the S-O line")
	}
}

// fullFlow drives a terminal panel through exchange, refusals, Enter, misdirected
// Hosts and WebSockets, then stops it; it returns every T, S and C it saw.
func fullFlow(t *testing.T, p *panel) (tokens, others []string) {
	t.Helper()
	tok := p.token(1)
	sess, csrf := p.exchange(tok)
	p.exchangeRaw(tok)
	p.exchangeRaw("malformed")
	tok2 := p.enter()
	sess2, _ := p.exchange(tok2)
	tok3 := p.enter()
	p.enter()
	p.exchangeRaw(tok3) // replaced: S-W
	host := "attacker.example"
	p.do(request{method: "GET", target: "/api/session", host: &host, header: [][2]string{{"Authorization", "Bearer " + sess}}})
	p.do(request{method: "GET", target: "/?session=" + sess})
	p.wsUpgrade(p.origin, []string{"intuitik.v1, csrf." + csrf + ", sess." + randomToken()})
	c := p.mustWS(sess2, csrf)
	roundTrip(c, "flow")
	p.interrupt()
	return []string{tok, tok2, tok3}, []string{sess, sess2, csrf}
}

// S10: stdout and stderr of a full run hold T only in S-O and S-R, and no S or C.
func TestS10_TokenNotLogged(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	tokens, others := fullFlow(t, p)
	problems := stderrCheck(p.stdout.bytes(), p.stderr.bytes(), tokens, others)
	// Every panel that ran before this test in the same run.
	allOutput.mu.Lock()
	var all []string
	for s := range allOutput.secrets {
		all = append(all, s)
	}
	for _, out := range allOutput.output {
		for _, s := range all {
			if n := countSecret(out, s); n != 0 {
				problems = append(problems, fmt.Sprintf("a session or CSRF value of an earlier panel appears %d time(s) in its output", n))
			}
		}
	}
	allOutput.mu.Unlock()
	t.Logf("S10 stderr check: %d tokens, %d session/CSRF values, %d earlier secrets, problems %d", len(tokens), len(others), len(all), len(problems))
	for _, pr := range problems {
		t.Error(pr)
	}
}

// S11: error responses do not return the request input.
func TestS11_NoReflection(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, csrf := p.exchange(p.token(1))
	const marker = "zq7Marker9x"
	host := marker + ".example"
	reqs := []request{
		{method: "GET", target: "/" + marker},
		{method: "GET", target: "/api/" + marker + "?" + marker + "=1"},
		{method: "GET", target: "/api/session", header: [][2]string{{"X-" + marker, marker}, {"Authorization", "Bearer " + marker}}},
		{method: "GET", target: "/", host: &host},
		{method: "POST", target: "/api/session", header: [][2]string{{"Origin", "http://" + marker}, {"Sec-Fetch-Site", "same-origin"}}, body: exchangeBody(marker)},
		{method: "POST", target: "/api/session", header: p.sameOrigin(), body: `{"t":` + marker},
		{method: "POST", target: "/api/ping", header: append(p.sameOrigin(), [2]string{"Authorization", "Bearer " + sess}, [2]string{"X-CSRF-Token", marker})},
		{method: "GET", target: "/api/ws", header: append(p.sameOrigin(), [2]string{"Sec-WebSocket-Protocol", "intuitik.v1, csrf." + csrf + ", sess." + sess + ", " + marker})},
		{method: "GET", target: "/api/ws", header: append(p.sameOrigin(), [2]string{"Upgrade", marker}, [2]string{"Connection", "Upgrade"}, [2]string{"Sec-WebSocket-Protocol", "intuitik.v1, csrf." + csrf + ", sess." + sess})},
		{method: marker, target: "/api/session"},
	}
	for _, r := range reqs {
		got := p.do(r)
		n := bytes.Count(got.raw, []byte(marker))
		t.Logf("S11 %s %s: %d, marker in response %d", r.method, r.target, got.status, n)
		if n != 0 {
			t.Errorf("%s %s reflects the marker %d time(s)", r.method, r.target, n)
		}
	}
}

// S12: a second exchange and an unused token after Enter are refused with S-W.
func TestS12_SecondExchangeAlarms(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	tok := p.token(1)
	p.exchange(tok)
	r := p.exchangeRaw(tok)
	p.stderr.waitFor(5*time.Second, func(s string) bool { return s == textSW })
	t.Logf("S12 second exchange: %d, S-W lines %d", r.status, p.stderr.count(textSW))
	if r.status != 403 || p.stderr.count(textSW) != 1 {
		t.Errorf("second exchange: %d with %d S-W line(s), want 403 and 1", r.status, p.stderr.count(textSW))
	}
	q := startPanel(t, panelOpts{terminal: true})
	unused := q.token(1)
	q.enter()
	r = q.exchangeRaw(unused)
	q.stderr.waitFor(5*time.Second, func(s string) bool { return s == textSW })
	t.Logf("S12 unused T after Enter: %d, S-W lines %d", r.status, q.stderr.count(textSW))
	if r.status != 403 || q.stderr.count(textSW) != 1 {
		t.Errorf("unused T after Enter: %d with %d S-W line(s), want 403 and 1", r.status, q.stderr.count(textSW))
	}
}

// S12: a token that expired unused is refused without S-W.
func TestS12_TokenExpires(t *testing.T) {
	p := startPanel(t, panelOpts{test: true, clock: true})
	tok := p.token(1)
	p.setClock(61)
	r := p.exchangeRaw(tok)
	time.Sleep(300 * time.Millisecond)
	t.Logf("S12 exchange at 61 s: %d, S-W lines %d", r.status, p.stderr.count(textSW))
	if r.status != 403 || p.stderr.count(textSW) != 0 || string(r.body) != textBRF {
		t.Errorf("expired token: %d, %d S-W line(s), body %q; want 403, 0, B-RF", r.status, p.stderr.count(textSW), r.body)
	}
}

// S13: the HTTP/2 preface and Upgrade: h2c are refused; the server speaks HTTP/1.1.
func TestS13_NoH2C(t *testing.T) {
	p := startPanel(t, panelOpts{})
	conn, err := net.DialTimeout("tcp", p.host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	head := make([]byte, 64)
	n, _ := io.ReadAtLeast(conn, head, 1)
	conn.Close()
	got := string(head[:n])
	t.Logf("S13 prior-knowledge preface answered %q", strings.SplitN(got, "\r\n", 2)[0])
	if !strings.HasPrefix(got, "HTTP/1.1 ") || strings.HasPrefix(got, "HTTP/1.1 101") {
		t.Errorf("preface answer %q, want an HTTP/1.1 refusal", got)
	}
	r := p.do(request{method: "GET", target: "/", header: [][2]string{
		{"Connection", "Upgrade, HTTP2-Settings"}, {"Upgrade", "h2c"}, {"HTTP2-Settings", "AAMAAABkAARAAAAAAAIAAAAA"}}})
	t.Logf("S13 Upgrade: h2c answered %d %s", r.status, r.proto)
	if r.status == 101 || r.proto != "HTTP/1.1" || r.status != 400 {
		t.Errorf("Upgrade: h2c answered %d %s, want 400 HTTP/1.1", r.status, r.proto)
	}
}

// S17: class 2 Hosts get a 400 with no body; the valid token stays exchangeable.
func TestS17_MisdirectedHostResponse(t *testing.T) {
	check := func(p *panel) []string {
		problems := misdirected(p, randomToken(), randomToken(), p.token(1)) // before a session
		sess, csrf := p.exchange(p.token(1))
		tok2 := p.enter()
		problems = append(problems, misdirected(p, sess, csrf, tok2)...) // after a session
		if r := p.exchangeRaw(tok2); r.status != 200 {
			problems = append(problems, fmt.Sprintf("T' after the misdirected requests: %d, want 200", r.status))
		}
		return problems
	}
	fixtureCheck(t, "TestS17_MisdirectedHostResponse", panelOpts{terminal: true}, check, "headers-before-host")
}

// S17: each refusal is the 403 with the B-RF body; no T, T', S or C anywhere in the
// raw response; the searcher finds S in a successful exchange (positive control).
func TestS17_NoSecretInResponse(t *testing.T) {
	check := func(p *panel) []string {
		var problems []string
		tok := p.token(1)
		secrets := []string{tok}
		refusals := map[string]response{
			"unknown token (before a session)":  p.exchangeRaw(randomToken()),
			"malformed body (before a session)": p.do(request{method: "POST", target: "/api/session", header: p.sameOrigin(), body: "{"}),
		}
		ok := p.exchangeRaw(tok)
		sess, csrf := p.exchangeBodyValues(ok)
		if n := countSecret(ok.raw, sess); n < 1 {
			problems = append(problems, "positive control: S not found in the successful exchange")
		} else {
			p.t.Logf("S17 positive control: S found %d time(s) in the successful exchange", n)
		}
		secrets = append(secrets, sess, csrf)
		refusals["consumed"] = p.exchangeRaw(tok)
		tok2 := p.enter()
		tok3 := p.enter()
		secrets = append(secrets, tok2, tok3)
		refusals["replaced by Enter"] = p.exchangeRaw(tok2)
		last := "A"
		if strings.HasSuffix(tok3, "A") {
			last = "B"
		}
		refusals["mismatched"] = p.exchangeRaw(tok3[:42] + last)
		p.setClock(61)
		refusals["expired"] = p.exchangeRaw(tok3)
		for name, r := range refusals {
			if r.status != 403 || string(r.body) != textBRF {
				problems = append(problems, fmt.Sprintf("%s: %d body %q, want 403 and B-RF", name, r.status, r.body))
			}
			problems = append(problems, prefixed(name, fixedHeaderProblems(r))...)
			for _, s := range secrets {
				if n := countSecret(r.raw, s); n != 0 {
					problems = append(problems, fmt.Sprintf("%s: a secret appears %d time(s) in the raw response", name, n))
				}
			}
		}
		return problems
	}
	fixtureCheck(t, "TestS17_NoSecretInResponse", panelOpts{test: true, terminal: true, clock: true}, check, "refusal-contains-token")
}

func prefixed(p string, list []string) []string {
	var out []string
	for _, s := range list {
		out = append(out, p+": "+s)
	}
	return out
}

// S18: Enter makes T' (different from T); the unused T is refused; T' works once.
func TestS18_OneTimeTokenRotation(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	tok := p.token(1)
	tok2 := p.enter()
	t.Logf("S18 rotation: T' differs from T: %v", tok != tok2)
	if tok == tok2 {
		t.Fatal("T' equals T")
	}
	if r := p.exchangeRaw(tok); r.status != 403 {
		t.Errorf("unused T after Enter: %d, want 403", r.status)
	}
	if r := p.exchangeRaw(tok2); r.status != 200 {
		t.Errorf("T': %d, want 200", r.status)
	}
	if r := p.exchangeRaw(tok2); r.status != 403 {
		t.Errorf("T' again: %d, want 403", r.status)
	}
}

// S18: S and C appear nowhere in stdout or stderr over a full run.
func TestS18_SessionNotLogged(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	tokens, others := fullFlow(t, p)
	problems := stderrCheck(p.stdout.bytes(), p.stderr.bytes(), tokens, others)
	t.Logf("S18 stderr check over a full run: problems %d", len(problems))
	for _, pr := range problems {
		t.Error(pr)
	}
}

// S18: order A (the browser first) and order B (the attacker first, the residual
// risk) each end with a 403 for the second party and one S-W.
func TestS18_ExchangeOrder(t *testing.T) {
	for _, order := range []string{"A browser first", "B attacker first"} {
		p := startPanel(t, panelOpts{})
		tok := p.token(1)
		first, _ := p.exchange(tok)
		second := p.exchangeRaw(tok)
		p.stderr.waitFor(5*time.Second, func(s string) bool { return s == textSW })
		n := countSecret(second.raw, first)
		t.Logf("S18 order %s: second party %d, S in its raw response %d, S-W %d", order, second.status, n, p.stderr.count(textSW))
		if second.status != 403 || n != 0 || p.stderr.count(textSW) != 1 {
			t.Errorf("order %s: %d, S %d, S-W %d; want 403, 0, 1", order, second.status, n, p.stderr.count(textSW))
		}
	}
}

// S18: the opener receives the address as one argument in the #t= form; T and T'
// appear only in the stand-in's argv, S and C in no argv; a failed open prints S-O.
func TestS18_ArgvExposure(t *testing.T) {
	dir := t.TempDir()
	counts := filepath.Join(dir, "argc.txt")
	urls := filepath.Join(dir, "argv1.txt")
	standin := filepath.Join(dir, "standin-opener")
	os.WriteFile(standin, []byte("#!/bin/sh\nprintf '%s\\n' \"$#\" >> '"+counts+"'\nprintf '%s\\n' \"$1\" >> '"+urls+"'\nsleep 3\n"), 0o700)
	p := startPanel(t, panelOpts{terminal: true, browser: &standin})
	p.openLog = urls
	snapshot := func(when string, secrets map[string]string) {
		var procs map[int][]string
		deadline := time.Now().Add(5 * time.Second)
		for {
			procs = cmdlines()
			found := false
			for _, argv := range procs {
				if len(argv) > 1 && argv[1] == standin {
					found = true
				}
			}
			if found || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		for name, s := range secrets {
			inStandin, elsewhere := 0, 0
			for _, argv := range procs {
				n := countSecret([]byte(strings.Join(argv, "\x00")), s)
				if len(argv) > 1 && argv[1] == standin {
					inStandin += n
				} else {
					elsewhere += n
				}
			}
			t.Logf("S18 argv %s: %s in the stand-in %d, elsewhere %d", when, name, inStandin, elsewhere)
			isToken := strings.HasPrefix(name, "T")
			if elsewhere != 0 || (!isToken && inStandin != 0) {
				t.Errorf("argv %s: %s appears in the stand-in %d and elsewhere %d time(s)", when, name, inStandin, elsewhere)
			}
		}
	}
	tok := p.token(1)
	snapshot("after the open", map[string]string{"T": tok})
	sess, csrf := p.exchange(tok)
	snapshot("after the exchange", map[string]string{"T": tok, "S": sess, "C": csrf})
	tok2 := p.enter()
	snapshot("after Enter", map[string]string{"T": tok, "T'": tok2, "S": sess, "C": csrf})
	rawCounts, _ := os.ReadFile(counts)
	raw, _ := os.ReadFile(urls)
	argc := strings.Fields(string(rawCounts))
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	t.Logf("S18 stand-in calls: %d, argument counts %v", len(args), argc)
	if len(argc) != len(args) || len(args) != 2 {
		t.Errorf("stand-in calls: %d counts, %d addresses; want 2 and 2 (start and Enter)", len(argc), len(args))
	}
	for i := range args {
		if i >= len(argc) || argc[i] != "1" || !loginURL.MatchString(args[i]) {
			t.Errorf("stand-in call %d: argument count %v, argument %q; want one argument http://127.0.0.1:P/#t=<T>", i+1, argc, args[i])
		}
	}
	if !strings.Contains(string(raw), tok) {
		t.Error("positive control: T not found in the stand-in arguments")
	}

	f := startPanel(t, panelOpts{browser: strPtr("/nonexistent/opener")})
	line := f.stderr.wait(t, "S-O", 10*time.Second, func(s string) bool { return strings.HasPrefix(s, textSO) })
	t.Logf("S18 open failure: S-O in the #t= form: %v; ?t= addresses %d", loginURL.MatchString(strings.TrimPrefix(line, textSO)), strings.Count(string(f.stderr.bytes()), "?t="))
	if !loginURL.MatchString(strings.TrimPrefix(line, textSO)) || strings.Contains(string(f.stderr.bytes()), "?t=") {
		t.Errorf("open failure line %q", line)
	}
}

// S18 with item P1 (a): BROWSER is one executable path started without a shell; a
// missing file prints S-O and the process keeps running; empty means the OS default.
func TestS18_BrowserEnv(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "opened.txt")
	odd := filepath.Join(dir, "open me; touch shell-ran $(touch subshell-ran)")
	os.WriteFile(odd, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+log+"'\n"), 0o700)
	p := startPanel(t, panelOpts{browser: &odd, dir: dir})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, _ := os.ReadFile(log); len(raw) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ := os.ReadFile(log)
	_, e1 := os.Stat(filepath.Join(dir, "shell-ran"))
	_, e2 := os.Stat(filepath.Join(dir, "subshell-ran"))
	t.Logf("S18 BROWSER with spaces and metacharacters: received %q; shell marker files exist: %v %v", strings.TrimSpace(string(raw)), e1 == nil, e2 == nil)
	if !loginURL.MatchString(strings.TrimSpace(string(raw))) || e1 == nil || e2 == nil {
		t.Errorf("BROWSER %q: received %q, markers %v %v", odd, raw, e1, e2)
	}
	time.Sleep(500 * time.Millisecond)
	okSO := strings.Count(string(p.stderr.bytes()), textSO)
	t.Logf("S18 BROWSER that exits 0: S-O lines %d (want 0)", okSO)
	if okSO != 0 {
		t.Errorf("an opener that exits 0 printed S-O %d times", okSO)
	}

	// An executable that starts, receives the address and then fails is an open
	// failure too: S-O with the same #t= address, no query address, still serving.
	failLog := filepath.Join(dir, "failed-opener.txt")
	failing := filepath.Join(dir, "failing-browser")
	os.WriteFile(failing, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+failLog+"'\nexit 3\n"), 0o700)
	fb := startPanel(t, panelOpts{browser: &failing})
	fbLine := fb.stderr.wait(t, "S-O", 10*time.Second, func(s string) bool { return strings.HasPrefix(s, textSO) })
	received, _ := os.ReadFile(failLog)
	fbAddr := strings.TrimPrefix(fbLine, textSO)
	fbStatus := fb.do(request{method: "GET", target: "/"}).status
	t.Logf("S18 BROWSER that starts and exits 3: S-O in the #t= form: %v; same address as the opener received: %v; ?t= addresses %d; still serving: GET / %d", loginURL.MatchString(fbAddr), fbAddr == strings.TrimSpace(string(received)), strings.Count(string(fb.stderr.bytes()), "?t="), fbStatus)
	if !loginURL.MatchString(fbAddr) || fbAddr != strings.TrimSpace(string(received)) || strings.Contains(string(fb.stderr.bytes()), "?t=") || fbStatus != 200 {
		t.Errorf("opener that starts and fails: S-O %q, received %d bytes, GET / %d", strings.Replace(fbLine, fbAddr, "<address>", 1), len(received), fbStatus)
	}

	m := startPanel(t, panelOpts{browser: strPtr(filepath.Join(dir, "missing-browser"))})
	line := m.stderr.wait(t, "S-O", 10*time.Second, func(s string) bool { return strings.HasPrefix(s, textSO) })
	time.Sleep(200 * time.Millisecond)
	r := m.do(request{method: "GET", target: "/"})
	t.Logf("S18 missing BROWSER: %q; still serving: GET / %d", strings.Replace(line, strings.TrimPrefix(line, textSO), "<address>", 1), r.status)
	if r.status != 200 {
		t.Errorf("after a missing BROWSER the process does not serve: %d", r.status)
	}

	defLog := filepath.Join(dir, "default-opener.txt")
	d := startPanel(t, panelOpts{test: true, browser: strPtr(""), env: []string{"INTUITIK_TEST_DEFAULT_OPENER=" + defLog}})
	deadline = time.Now().Add(10 * time.Second)
	for {
		if raw, _ := os.ReadFile(defLog); len(raw) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := os.ReadFile(defLog)
	t.Logf("S18 empty BROWSER: the OS default opener seam received a login address: %v", loginURL.MatchString(strings.TrimSpace(string(got))))
	if !loginURL.MatchString(strings.TrimSpace(string(got))) {
		t.Errorf("empty BROWSER did not call the default opener: %q", got)
	}
	_ = d
}

// S19: after Enter, tab A (S1) and tab B (S2) both keep API, CSRF POST and
// WebSocket; tab A's open WebSocket stays; C is one per run.
func TestS19_ReconnectKeepsExistingTab(t *testing.T) {
	check := func(p *panel) []string {
		var problems []string
		s1, c1 := p.exchange(p.token(1))
		ws1 := p.mustWS(s1, c1)
		tok2 := p.enter()
		s2, c2 := p.exchangeBodyValues(p.exchangeRaw(tok2))
		if s1 == s2 {
			problems = append(problems, "S2 equals S1")
		}
		if c1 != c2 {
			problems = append(problems, "C changed after Enter")
		}
		for name, s := range map[string]string{"S1": s1, "S2": s2} {
			if r := p.apiGet("/api/session", s); r.status != 200 {
				problems = append(problems, fmt.Sprintf("%s GET: %d", name, r.status))
			}
			if r := p.ping(s, c1); r.status != 200 {
				problems = append(problems, fmt.Sprintf("%s POST with C: %d", name, r.status))
			}
			if c, _, err := p.dialWS(s, c1); err != nil {
				problems = append(problems, fmt.Sprintf("%s new WebSocket: %v", name, err))
			} else {
				if err := roundTrip(c, name); err != nil {
					problems = append(problems, fmt.Sprintf("%s new WebSocket round trip: %v", name, err))
				}
				c.CloseNow()
			}
		}
		if err := roundTrip(ws1, "tab A"); err != nil {
			problems = append(problems, fmt.Sprintf("tab A's existing WebSocket: %v", err))
		}
		unused := p.enter()
		p.enter()
		if r := p.exchangeRaw(unused); r.status != 403 {
			problems = append(problems, fmt.Sprintf("an unused token before Enter: %d, want 403", r.status))
		}
		return problems
	}
	fixtureCheck(t, "TestS19_ReconnectKeepsExistingTab", panelOpts{terminal: true}, check, "enter-discards-sessions", "enter-changes-csrf")
}

// S19: one request per cell of the state table.
func TestS19_StateTable(t *testing.T) {
	p := startPanel(t, panelOpts{test: true, terminal: true, clock: true})
	host := "attacker.example"
	cell := func(row, column string, send func() response, status int, csd *bool, alarm bool) response {
		t.Helper()
		alarmsBefore := p.stderr.count(textSW)
		r := send()
		ok := r.status == status
		if csd != nil && (len(r.header.Values("Clear-Site-Data")) == 1) != *csd {
			ok = false
		}
		if alarm {
			p.stderr.waitFor(5*time.Second, func(s string) bool { return p.stderr.count(textSW) > alarmsBefore })
		} else {
			time.Sleep(150 * time.Millisecond)
			if p.stderr.count(textSW) != alarmsBefore {
				ok = false
			}
		}
		t.Logf("S19 state %q, %s: %d (want %d), Clear-Site-Data %q, S-W %v; ok %v", row, column, r.status, status, r.header.Values("Clear-Site-Data"), alarm, ok)
		if !ok {
			t.Errorf("state %q, %s: does not match the table", row, column)
		}
		return r
	}
	yes, no := true, false

	tok := p.token(1)
	row := "just started"
	get := func(target string, h ...[2]string) func() response {
		return func() response { return p.do(request{method: "GET", target: target, header: h}) }
	}
	other := func(target string, h ...[2]string) func() response {
		return func() response { return p.do(request{method: "GET", target: target, host: &host, header: h}) }
	}
	exchange := func(tok string) func() response { return func() response { return p.exchangeRaw(tok) } }
	api := func(s string) func() response { return func() response { return p.apiGet("/api/session", s) } }

	cell(row, "no session: static", get("/"), 200, &yes, false)
	cell(row, "no session: API", get("/api/session"), 401, nil, false)
	cell(row, "other Host", other("/"), 400, nil, false)
	s1, _ := p.exchangeBodyValues(cell(row, "POST T", exchange(tok), 200, nil, false))

	row = "after T consumed"
	cell(row, "POST T again", exchange(tok), 403, nil, true)
	cell(row, "valid session", api(s1), 200, nil, false)
	cell(row, "no session: static", get("/"), 200, &no, false)
	cell(row, "no session: API", get("/api/session"), 401, nil, false)
	cell(row, "other Host", other("/api/session", [2]string{"Authorization", "Bearer " + s1}), 400, nil, false)

	row = "after Enter"
	unused := p.enter()
	tok3 := p.enter()
	cell(row, "unused earlier token", exchange(unused), 403, nil, true)
	s3, _ := p.exchangeBodyValues(cell(row, "POST T'", exchange(tok3), 200, nil, false))
	cell(row, "existing session", api(s1), 200, nil, false)
	cell(row, "new session", api(s3), 200, nil, false)
	cell(row, "no session: static", get("/"), 200, &no, false)
	cell(row, "no session: API", get("/api/ping"), 401, nil, false)
	cell(row, "other Host", other("/"), 400, nil, false)

	row = "T' expired, no exchange"
	tok4 := p.enter()
	p.setClock(61)
	cell(row, "POST expired T'", exchange(tok4), 403, nil, false)
	cell(row, "existing session", api(s3), 200, nil, false)
	cell(row, "no session: static", get("/"), 200, &no, false)
	cell(row, "no session: API", get("/api/session"), 401, nil, false)
	cell(row, "other Host", other("/"), 400, nil, false)

	t.Log(`S19 state "listener lost": the process exits`)
	listenerLoss(t, p)
}
