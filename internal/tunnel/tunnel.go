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
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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
	// hops are the SSH servers this goes through, each reached from the one
	// before it. The last is the server the target is dialled from; where
	// there are no jump hosts it is the only one.
	hops     []*ssh.Client
	listener net.Listener
	target   string

	once sync.Once
	err  error
}

// server is the last hop, which is the one that dials the target.
func (t *Tunnel) server() *ssh.Client { return t.hops[len(t.hops)-1] }

// Open dials the SSH server — through any jump hosts first — listens on a
// local port, and carries whatever arrives there to the target.
func Open(ctx context.Context, cfg Config) (*Tunnel, error) {
	if cfg.SSH.Host == "" {
		return nil, errors.New("this tunnel has no server to go through")
	}
	if cfg.Target == "" {
		return nil, errors.New("this tunnel has nowhere to carry anything to")
	}
	auth, closeAgent, err := authOf(cfg.SSH, cfg.Secret)
	if err != nil {
		return nil, err
	}
	if closeAgent != nil {
		// Held until every hop has signed in, because each one asks: an
		// agent closed after the first would leave the rest with nothing to
		// prove who this is.
		defer closeAgent.Close()
	}
	known, err := knownHosts(cfg.KnownHosts)
	if err != nil {
		return nil, err
	}

	var hops []*ssh.Client
	drop := func() {
		for i := len(hops) - 1; i >= 0; i-- {
			hops[i].Close()
		}
	}
	// Each hop is dialled from the one before it; the first is dialled from
	// here, with the context, so a server that never answers does not
	// outlast whoever asked for it.
	dial := func(addr string) (net.Conn, error) {
		if len(hops) == 0 {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
		return hops[len(hops)-1].Dial("tcp", addr)
	}

	// The jump hosts, in the order they were given. Every one of them has its
	// key checked exactly as the last will: a hop nobody verified is a hop
	// that can read everything passing through it (ADR-0109).
	for _, jump := range cfg.SSH.JumpHosts {
		user, addr := hopOf(jump, cfg.SSH.User)
		c, err := hop(dial, addr, user, auth, known)
		if err != nil {
			drop()
			return nil, fmt.Errorf("the jump host %s: %w", addr, err)
		}
		hops = append(hops, c)
	}

	// And the server itself, reached through the last of them.
	port := cfg.SSH.Port
	if port == 0 {
		port = 22
	}
	c, err := hop(dial, net.JoinHostPort(cfg.SSH.Host, strconv.Itoa(port)), cfg.SSH.User, auth, known)
	if err != nil {
		drop()
		return nil, err
	}
	hops = append(hops, c)

	// The local end. Loopback only: a tunnel is for this application, and a
	// listener on every interface would carry anything on the network into
	// the database at the other end.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		drop()
		return nil, err
	}
	t := &Tunnel{hops: hops, listener: listener, target: cfg.Target}
	go t.serve()
	return t, nil
}

// hop signs in to one SSH server, reached however dial reaches it.
func hop(dial func(string) (net.Conn, error), addr, user string,
	auth []ssh.AuthMethod, known ssh.HostKeyCallback) (*ssh.Client, error) {
	c, err := dial(addr)
	if err != nil {
		return nil, fmt.Errorf("could not be reached: %w", err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(c, addr,
		&ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: known})
	if err != nil {
		c.Close()
		return nil, err
	}
	return ssh.NewClient(sc, chans, reqs), nil
}

// hopOf reads a jump host written as a host, a host and port, or a user with
// either — falling back to the tunnel's own user and to 22, which is what
// every other SSH client does with the same text.
func hopOf(jump, user string) (string, string) {
	if at := strings.LastIndex(jump, "@"); at >= 0 {
		user, jump = jump[:at], jump[at+1:]
	}
	if _, _, err := net.SplitHostPort(jump); err != nil {
		jump = net.JoinHostPort(jump, "22")
	}
	return user, jump
}

// Addr is the local address to dial instead of the target.
func (t *Tunnel) Addr() string { return t.listener.Addr().String() }

// Close stops listening and drops the connection to the server. It is safe to
// call more than once, because whoever closes a source should not have to
// know whether something else already did.
func (t *Tunnel) Close() error {
	t.once.Do(func() {
		t.err = t.listener.Close()
		// In reverse: a hop is reached through the one before it, so the far
		// end goes first and the near end last.
		for i := len(t.hops) - 1; i >= 0; i-- {
			if err := t.hops[i].Close(); t.err == nil {
				t.err = err
			}
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
	remote, err := t.server().Dial("tcp", t.target)
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
// The closer it gives back is the connection to an agent, where one is being
// asked, and is held until every hop has signed in.
func authOf(cfg source.SSHConfig, secret func(string) (string, error)) ([]ssh.AuthMethod, io.Closer, error) {
	if secret == nil {
		secret = func(string) (string, error) { return "", nil }
	}
	switch cfg.Method {
	case "password":
		pw, err := secret("ssh_password")
		if err != nil {
			return nil, nil, fmt.Errorf("the tunnel's password could not be read: %w", err)
		}
		if pw == "" {
			return nil, nil, errors.New("this tunnel signs in with a password and none was given")
		}
		return []ssh.AuthMethod{ssh.Password(pw)}, nil, nil

	case "key":
		signer, err := signerOf(cfg.KeyFile, secret)
		if err != nil {
			return nil, nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil

	case "agent":
		conn, err := agentConn()
		if err != nil {
			return nil, nil, err
		}
		// Asked when the server wants them rather than now: an agent can be
		// unlocked between opening this and being asked, and a list taken
		// too early would be an empty one remembered.
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, conn, nil
	}
	return nil, nil, fmt.Errorf("a tunnel signs in with a password, a key or an agent, and %q is none of those", cfg.Method)
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

// agentConn reaches the agent this session is running under.
//
// SSH_AUTH_SOCK is how every other SSH client finds it, and an agent that is
// not running is said plainly: the alternative is a connection that fails
// later for what looks like the wrong reason.
func agentConn() (net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("this tunnel signs in through an agent and there is none running: SSH_AUTH_SOCK names nothing")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("the agent at %s could not be reached: %w", sock, err)
	}
	return conn, nil
}
