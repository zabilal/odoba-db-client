package tunnel

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A tunnel proved against an SSH server running in this process (T2.85,
// FR-1.9). x/crypto/ssh is both ends of this, so none of it needs a container
// and nothing leaves loopback.

// echo is something worth reaching through a tunnel: it sends back what it is
// sent, so a test can tell that bytes really went there and really came back.
func echo(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return l.Addr().String()
}

// forwarded is the payload of a direct-tcpip request: where the client wants
// the server to carry this channel.
type forwarded struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

// server runs an SSH server that carries connections onward, and returns its
// address with a known_hosts file recording the key it offers.
func server(t *testing.T, cfg *ssh.ServerConfig) (addr, known string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddHostKey(signer)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go serve(c, cfg)
		}
	}()

	known = filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{l.Addr().String()}, signer.PublicKey())
	if err := os.WriteFile(known, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return l.Addr().String(), known
}

// serve is one connection to the test server: authenticate, then carry
// whatever channels are asked for.
func serve(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "direct-tcpip" {
			ch.Reject(ssh.UnknownChannelType, "only forwarding here")
			continue
		}
		var want forwarded
		if err := ssh.Unmarshal(ch.ExtraData(), &want); err != nil {
			ch.Reject(ssh.ConnectionFailed, "unreadable")
			continue
		}
		go carryOn(ch, want)
	}
}

func carryOn(ch ssh.NewChannel, want forwarded) {
	target := net.JoinHostPort(want.Host, strconv.Itoa(int(want.Port)))
	remote, err := net.Dial("tcp", target)
	if err != nil {
		ch.Reject(ssh.ConnectionFailed, "nothing there")
		return
	}
	defer remote.Close()
	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(reqs)
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, channel); done <- struct{}{} }()
	go func() { io.Copy(channel, remote); done <- struct{}{} }()
	<-done
}

// reaches sends something through the tunnel and gives back what came out,
// which is the only proof that a tunnel is carrying anything.
func reaches(t *testing.T, tn *Tunnel, said string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", tn.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dialling the tunnel: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, said); err != nil {
		t.Fatalf("writing through the tunnel: %v", err)
	}
	heard := make([]byte, len(said))
	if _, err := io.ReadFull(c, heard); err != nil {
		t.Fatalf("reading back through the tunnel: %v", err)
	}
	return string(heard)
}

// keyPair writes a private key to a file, under a passphrase where one is
// given, and returns the path and the public key the server should accept.
func keyPair(t *testing.T, passphrase string) (path string, public ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return path, signer.PublicKey()
}

func secrets(values map[string]string) func(string) (string, error) {
	return func(key string) (string, error) { return values[key], nil }
}

func TestATunnelCarriesWhatItIsGiven(t *testing.T) {
	target := echo(t)
	addr, known := server(t, &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == "open sesame" {
				return nil, nil
			}
			return nil, errors.New("no")
		},
	})
	host, port := split(t, addr)

	tn, err := Open(context.Background(), Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "password"},
		Target:     target,
		Secret:     secrets(map[string]string{"ssh_password": "open sesame"}),
		KnownHosts: known,
	})
	if err != nil {
		t.Fatalf("opening the tunnel: %v", err)
	}
	defer tn.Close()

	// The local end is loopback and nowhere else: a tunnel is for this
	// application, not for whatever else is on the network.
	if !strings.HasPrefix(tn.Addr(), "127.0.0.1:") {
		t.Errorf("the tunnel listens on %s", tn.Addr())
	}
	if got := reaches(t, tn, "hello through the tunnel"); got != "hello through the tunnel" {
		t.Errorf("what came back was %q", got)
	}
	// And it carries more than one connection, because a driver opens several.
	if got := reaches(t, tn, "and again"); got != "and again" {
		t.Errorf("a second connection got %q", got)
	}
}

func TestATunnelSignsInWithAKey(t *testing.T) {
	target := echo(t)
	path, public := keyPair(t, "")
	addr, known := server(t, &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, offered ssh.PublicKey) (*ssh.Permissions, error) {
			if string(offered.Marshal()) == string(public.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("not that key")
		},
	})
	host, port := split(t, addr)

	tn, err := Open(context.Background(), Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "key", KeyFile: path},
		Target:     target,
		KnownHosts: known,
	})
	if err != nil {
		t.Fatalf("opening the tunnel with a key: %v", err)
	}
	defer tn.Close()
	if got := reaches(t, tn, "signed in"); got != "signed in" {
		t.Errorf("what came back was %q", got)
	}
}

func TestATunnelSignsInWithAKeyUnderAPassphrase(t *testing.T) {
	target := echo(t)
	path, public := keyPair(t, "let me in")
	addr, known := server(t, &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, offered ssh.PublicKey) (*ssh.Permissions, error) {
			if string(offered.Marshal()) == string(public.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("not that key")
		},
	})
	host, port := split(t, addr)
	cfg := Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "key", KeyFile: path},
		Target:     target,
		Secret:     secrets(map[string]string{"ssh_passphrase": "let me in"}),
		KnownHosts: known,
	}

	tn, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("opening the tunnel with a key under a passphrase: %v", err)
	}
	defer tn.Close()
	if got := reaches(t, tn, "unlocked"); got != "unlocked" {
		t.Errorf("what came back was %q", got)
	}

	// Without the passphrase it says which thing is missing, rather than
	// failing as though the key itself were wrong.
	cfg.Secret = secrets(nil)
	if _, err := Open(context.Background(), cfg); err == nil ||
		!strings.Contains(err.Error(), "passphrase") {
		t.Errorf("a locked key with no passphrase: %v", err)
	}
	// And a wrong one says that, rather than blaming the key.
	cfg.Secret = secrets(map[string]string{"ssh_passphrase": "wrong"})
	if _, err := Open(context.Background(), cfg); err == nil ||
		!strings.Contains(err.Error(), "does not open it") {
		t.Errorf("a locked key with the wrong passphrase: %v", err)
	}
}

func TestAHostNobodyKnowsIsRefusedRatherThanTrusted(t *testing.T) {
	target := echo(t)
	addr, _ := server(t, &ssh.ServerConfig{NoClientAuth: true})
	host, port := split(t, addr)

	// A known_hosts with nothing in it: the server is real, and there is no
	// way to tell it from something answering in its place.
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "password"},
		Target:     target,
		Secret:     secrets(map[string]string{"ssh_password": "anything"}),
		KnownHosts: empty,
	})
	if err == nil {
		t.Fatal("a host nobody knows was trusted")
	}
	// The refusal says what to do about it, because being told no without
	// being told why is how somebody ends up disabling the check.
	for _, want := range []string{"not a host", "accepting its key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

func TestAHostWhoseKeyChangedIsSaidDifferently(t *testing.T) {
	target := echo(t)
	addr, _ := server(t, &ssh.ServerConfig{NoClientAuth: true})
	host, port := split(t, addr)

	// known_hosts naming this address, with somebody else's key against it.
	_, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{addr}, signer.PublicKey())
	if err := os.WriteFile(stale, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "password"},
		Target:     target,
		Secret:     secrets(map[string]string{"ssh_password": "anything"}),
		KnownHosts: stale,
	})
	if err == nil {
		t.Fatal("a host whose key changed was trusted")
	}
	// A changed key is not the same problem as an unknown host, and reading
	// the wrong one of those sends somebody the wrong way.
	if !strings.Contains(err.Error(), "interception") {
		t.Errorf("a changed key reads as: %v", err)
	}
}

func TestWhatATunnelWillNotEvenTry(t *testing.T) {
	// None of these reaches a server, and none of them should: each is wrong
	// before there is anything to dial.
	//
	// What each one says is checked rather than merely that it said
	// something. These configurations would also fail at the first hostname
	// lookup, so a test asking only whether something went wrong would pass
	// just as happily on the wrong failure.
	for name, c := range map[string]struct {
		cfg  Config
		says string
	}{
		"no server to go through": {
			Config{Target: "127.0.0.1:1"}, "no server to go through"},
		"nowhere to carry anything": {
			Config{SSH: source.SSHConfig{Host: "h", Method: "password"}}, "nowhere to carry"},
		"a way of signing in nobody has": {
			Config{SSH: source.SSHConfig{Host: "h", Method: "semaphore"}, Target: "127.0.0.1:1"}, "none of those"},
		"an agent, which is not written": {
			Config{SSH: source.SSHConfig{Host: "h", Method: "agent"}, Target: "127.0.0.1:1"}, "not written yet"},
		"a password nobody gave": {
			Config{SSH: source.SSHConfig{Host: "h", Method: "password"}, Target: "127.0.0.1:1"}, "none was given"},
		"a key nobody named": {
			Config{SSH: source.SSHConfig{Host: "h", Method: "key"}, Target: "127.0.0.1:1"}, "none was named"},
	} {
		_, err := Open(context.Background(), c.cfg)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s is refused with %q, which does not say %q", name, err, c.says)
		}
	}
}

func TestClosingATunnelTwiceIsNotAnError(t *testing.T) {
	// Whoever closes a source should not have to know whether something else
	// already closed the tunnel under it.
	target := echo(t)
	addr, known := server(t, &ssh.ServerConfig{NoClientAuth: true})
	host, port := split(t, addr)
	tn, err := Open(context.Background(), Config{
		SSH:        source.SSHConfig{Host: host, Port: port, User: "somebody", Method: "password"},
		Target:     target,
		Secret:     secrets(map[string]string{"ssh_password": "anything"}),
		KnownHosts: known,
	})
	if err != nil {
		t.Fatalf("opening the tunnel: %v", err)
	}
	if err := tn.Close(); err != nil {
		t.Errorf("closing a tunnel: %v", err)
	}
	if err := tn.Close(); err != nil {
		t.Errorf("closing it again: %v", err)
	}
	// And nothing goes through it afterwards.
	if c, err := net.DialTimeout("tcp", tn.Addr(), time.Second); err == nil {
		c.Close()
		t.Error("a closed tunnel still accepts connections")
	}
}

func split(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return host, n
}
