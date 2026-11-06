package helper_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/helper"
)

const sampleAnswer = "tok-0123456789"

// fakeLauncher answers the helper socket with answer, or, for an empty
// answer, with the token for the host "dustpan.example.com" and no token
// for any other host. It returns the path of the socket and the kinds of
// the requests that it got.
func fakeLauncher(t *testing.T, answer string) (string, func() []string) {
	t.Helper()

	sock := filepath.Join(t.TempDir(), launcher.SocketHelper)

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu    sync.Mutex
		kinds []string
		done  = make(chan struct{})
	)

	go func() {
		defer close(done)

		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}

			req := serve(t, conn, answer)

			mu.Lock()
			kinds = append(kinds, req.Kind)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Errorf("close the socket: %v", closeErr)
		}

		<-done
	})

	return sock, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), kinds...)
	}
}

// serve reads one request on conn and writes the answer.
func serve(t *testing.T, conn net.Conn, answer string) helper.Request {
	t.Helper()

	var req helper.Request

	line, readErr := bufio.NewReader(conn).ReadBytes('\n')
	if readErr != nil || json.Unmarshal(line, &req) != nil {
		t.Errorf("read the request %q: %v", line, readErr)
	}

	out := answer
	if out == "" {
		out = `{}`
		if req.Host == "dustpan.example.com" {
			out = `{"token":"` + sampleAnswer + `"}`
		}
	}

	if _, err := conn.Write([]byte(out)); err != nil {
		t.Errorf("write the answer: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Errorf("close the connection: %v", err)
	}

	return req
}

// call runs one helper call and returns its output.
func call(t *testing.T, sock string, mode launcher.Mode, stdin string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := helper.Run(t.Context(), &helper.Call{
		Mode: mode, Args: args, Stdin: strings.NewReader(stdin), Stdout: &out, Socket: sock, Dial: helper.UnixDialer,
	})

	return out.String(), err
}

func TestOpenTofuHelper(t *testing.T) {
	t.Parallel()

	sock, kinds := fakeLauncher(t, "")

	cases := []struct {
		name string
		args []string
		want string
		err  error
	}{
		{"server host", []string{"get", "dustpan.example.com"}, `{"token":"` + sampleAnswer + `"}` + "\n", nil},
		{"other host", []string{"get", "registry.example.com"}, "{}\n", nil},
		{"store", []string{"store", "dustpan.example.com"}, "", helper.ErrUsage},
		{"forget", []string{"forget", "dustpan.example.com"}, "", helper.ErrUsage},
		{"no host", []string{"get"}, "", helper.ErrUsage},
		{"host with a line break", []string{"get", "a\nb"}, "", helper.ErrUsage},
		{"host with a path", []string{"get", "a/b"}, "", helper.ErrUsage},
	}

	for _, tc := range cases {
		got, err := call(t, sock, launcher.ModeOpenTofuHelper, "", tc.args...)
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("%s: helper = %q, %v; want %q, %v", tc.name, got, err, tc.want, tc.err)
		}
	}

	if got := strings.Join(kinds(), ","); got != "opentofu,opentofu" {
		t.Fatalf("requests = %s, want two requests of the kind opentofu", got)
	}
}

func TestGitHelper(t *testing.T) {
	t.Parallel()

	sock, kinds := fakeLauncher(t, "")
	creds := "username=x-access-token\npassword=" + sampleAnswer + "\n"

	cases := []struct {
		name  string
		op    string
		stdin string
		want  string
		err   error
	}{
		{"https host", "get", "protocol=https\nhost=dustpan.example.com\n\n", creds, nil},
		{"other host", "get", "protocol=https\nhost=other.example.com\n", "", nil},
		{"plain http gets nothing", "get", "protocol=http\nhost=dustpan.example.com\n", "", nil},
		{"store does nothing", "store", "protocol=https\nhost=dustpan.example.com\npassword=x\n", "", nil},
		{"erase does nothing", "erase", "protocol=https\nhost=dustpan.example.com\n", "", nil},
		{"unknown operation", "list", "", "", helper.ErrUsage},
		{"line without a key", "get", "protocol=https\nnothing\n", "", helper.ErrUsage},
		{"hostile host", "get", "protocol=https\nhost=a b\n", "", helper.ErrUsage},
	}

	for _, tc := range cases {
		got, err := call(t, sock, launcher.ModeGitHelper, tc.stdin, tc.op)
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("%s: helper = %q, %v; want %q, %v", tc.name, got, err, tc.want, tc.err)
		}
	}

	if got := strings.Join(kinds(), ","); got != "git,git" {
		t.Fatalf("requests = %s, want two requests of the kind git", got)
	}
}

func TestHelperFailsClosed(t *testing.T) {
	t.Parallel()

	badSock, _ := fakeLauncher(t, `{"token":"a\nb"}`)
	notJSON, _ := fakeLauncher(t, `not json`)

	cases := []struct {
		name string
		sock string
		mode launcher.Mode
		err  error
	}{
		{"no socket variable", "", launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"relative socket", "helper.sock", launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"other file name", "/tmp/other.sock", launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"no launcher", "/nonexistent/" + launcher.SocketHelper, launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"answer with a line break", badSock, launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"answer not JSON", notJSON, launcher.ModeOpenTofuHelper, helper.ErrNoLauncher},
		{"not a helper mode", badSock, launcher.ModeRun, helper.ErrUsage},
	}

	for _, tc := range cases {
		got, err := call(t, tc.sock, tc.mode, "", "get", "dustpan.example.com")
		if !errors.Is(err, tc.err) || got != "" {
			t.Errorf("%s: helper = %q, %v; want no output and %v", tc.name, got, err, tc.err)
		}
	}
}
