// Package tunnel carries a database connection through an SSH server
// (FR-1.9).
//
// What it proves before carrying anything is ADR-0109: the server's host key
// is checked against the user's own known_hosts, and a host that is not in
// there is refused rather than trusted. Everything the database connection
// carries — the credentials it authenticates with and every row it reads —
// goes through here, so a tunnel that took whatever key it was offered would
// hand all of it to anything able to answer on that address.
//
// No driver knows any of this exists. A tunnel is opened before the driver
// sees its configuration, and the driver is handed a host and port that point
// at the local end.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Config is what opening a tunnel needs.
type Config struct {
	// SSH is the server to go through.
	SSH source.SSHConfig

	// Target is the address to reach through it, as the driver would have
	// dialled it directly.
	Target string

	// Secret resolves a password or a key's passphrase from the keychain, as
	// it does for a driver's own credentials (FR-1.5).
	Secret func(key string) (string, error)

	// KnownHosts is the file to check the server's key against. Empty means
	// the user's own, which is where every other SSH client has already
	// answered this question (ADR-0109).
	KnownHosts string
}

// Tunnel is a local address that carries what it is given to somewhere else.
type Tunnel struct {
	client   *ssh.Client
	listener net.Listener
	target   string

	once sync.Once
	err  error
}

// Open dials the SSH server, listens on a local port, and carries whatever
// arrives there to the target through the server.
func Open(ctx context.Context, cfg Config) (*Tunnel, error) {
	if cfg.SSH.Host == "" {
		return nil, errors.New("this tunnel has no server to go through")
	}
	if cfg.Target == "" {
		return nil, errors.New("this tunnel has nowhere to carry anything to")
	}
	auth, err := authOf(cfg.SSH, cfg.Secret)
	if err != nil {
		return nil, err
	}
	known, err := knownHosts(cfg.KnownHosts)
	if err != nil {
		return nil, err
	}

	port := cfg.SSH.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(cfg.SSH.Host, strconv.Itoa(port))

	// Dialled with the context so that a server which never answers does not
	// outlast whoever asked for it.
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("the tunnel's own server could not be reached: %w", err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: cfg.SSH.User, Auth: auth, HostKeyCallback: known,
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(sc, chans, reqs)

	// The local end. Loopback only: a tunnel is for this application, and a
	// listener on every interface would carry anything on the network into
	// the database at the other end.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, err
	}
	t := &Tunnel{client: client, listener: listener, target: cfg.Target}
	go t.serve()
	return t, nil
}

// Addr is the local address to dial instead of the target.
func (t *Tunnel) Addr() string { return t.listener.Addr().String() }

// Close stops listening and drops the connection to the server. It is safe to
// call more than once, because whoever closes a source should not have to
// know whether something else already did.
func (t *Tunnel) Close() error {
	t.once.Do(func() {
		t.err = t.listener.Close()
		if err := t.client.Close(); t.err == nil {
			t.err = err
		}
	})
	return t.err
}

// serve carries every connection the local end accepts, until it is closed.
func (t *Tunnel) serve() {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return // closed, which is the only way out
		}
		go t.carry(local)
	}
}

// carry joins one local connection to one through the server.
func (t *Tunnel) carry(local net.Conn) {
	defer local.Close()
	remote, err := t.client.Dial("tcp", t.target)
	if err != nil {
		return
	}
	defer remote.Close()

	// Either direction ending ends the pair: a half-closed tunnel would leave
	// a driver waiting on a connection that has nothing behind it.
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

// authOf is how this proves who it is to the server.
func authOf(cfg source.SSHConfig, secret func(string) (string, error)) ([]ssh.AuthMethod, error) {
	if secret == nil {
		secret = func(string) (string, error) { return "", nil }
	}
	switch cfg.Method {
	case "password":
		pw, err := secret("ssh_password")
		if err != nil {
			return nil, fmt.Errorf("the tunnel's password could not be read: %w", err)
		}
		if pw == "" {
			return nil, errors.New("this tunnel signs in with a password and none was given")
		}
		return []ssh.AuthMethod{ssh.Password(pw)}, nil

	case "key":
		signer, err := signerOf(cfg.KeyFile, secret)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil

	case "agent":
		return nil, errors.New("signing in through an agent is not written yet")
	}
	return nil, fmt.Errorf("a tunnel signs in with a password, a key or an agent, and %q is none of those", cfg.Method)
}

// signerOf reads a private key, asking for its passphrase only if the key
// turns out to have one. A key that needs no passphrase must not demand one.
func signerOf(path string, secret func(string) (string, error)) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("this tunnel signs in with a key and none was named")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the tunnel's key could not be read: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err == nil {
		return signer, nil
	}
	var locked *ssh.PassphraseMissingError
	if !errors.As(err, &locked) {
		return nil, fmt.Errorf("the tunnel's key could not be read as one: %w", err)
	}

	pass, perr := secret("ssh_passphrase")
	if perr != nil {
		return nil, fmt.Errorf("the key's passphrase could not be read: %w", perr)
	}
	if pass == "" {
		return nil, errors.New("this key is kept under a passphrase and none was given")
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(pass))
	if err != nil {
		return nil, errors.New("this key's passphrase does not open it")
	}
	return signer, nil
}

// knownHosts checks the server's key against the file where every other SSH
// client on this machine has already answered the question (ADR-0109).
func knownHosts(path string) (ssh.HostKeyCallback, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("there is no home directory to find known_hosts in: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	check, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("%s could not be read, and a tunnel will not go anywhere it cannot check: %w", path, err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		// A key that changed and a host never seen are different problems and
		// are said differently: the first is what an interception looks like.
		var mismatch *knownhosts.KeyError
		if errors.As(err, &mismatch) && len(mismatch.Want) > 0 {
			return fmt.Errorf("the key %s offered is not the one %s records for it, "+
				"which is what an interception looks like, so nothing was sent; "+
				"if that server was genuinely rebuilt, remove its line from %s first",
				hostname, path, path)
		}
		return fmt.Errorf("%s is not a host %s knows, so there is no way to tell it "+
			"from something answering in its place; add it as any SSH client would, "+
			"by connecting to it once and accepting its key", hostname, path)
	}, nil
}
