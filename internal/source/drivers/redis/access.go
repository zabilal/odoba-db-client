package redis

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a command does (NFR-S4, T2.44).
//
// The guard has to know what a command would do before it runs, and Redis has
// no grammar to read that from: a command is a name, and the name is the
// whole of it. So the names are listed — the ones that only read, and the
// ones that change the server rather than what it holds — and everything else
// is a write.
//
// The list is read the safe direction (source.Dialect's own rule): a name
// nobody listed is a write, and an unlisted subcommand of a command that
// administers the server is a change to the server. A missing name costs a
// person a confirmation; a wrong one would cost them their data.

// readOnly are the commands that only read.
var readOnly = words(`
	get getrange mget strlen substr lcs
	exists ttl pttl type randomkey keys scan dump expiretime pexpiretime
	hget hmget hgetall hkeys hvals hlen hexists hstrlen hscan hrandfield
	lrange llen lindex lpos
	smembers scard sismember smismember srandmember sscan sinter sintercard sunion sdiff
	zscore zmscore zcard zcount zrange zrangebyscore zrangebylex zrevrange zrevrangebyscore
	zrevrangebylex zrank zrevrank zscan zrandmember zdiff zinter zintercard zunion zlexcount
	xrange xrevrange xlen xpending
	bitcount bitpos getbit
	geodist geopos geohash geosearch georadius_ro georadiusbymember_ro
	pfcount
	json.get json.mget json.type json.arrlen json.arrindex json.objkeys json.objlen json.strlen json.resp
	ft.search ft.aggregate ft.info ft.explain
	info dbsize ping echo time lastsave lolwut wait select auth hello reset
`)

// serverChanging are the commands that change the server rather than what it
// holds: what is kept, who may connect, whether it is still running. They are
// guarded as a structural change is, because that is what they are.
var serverChanging = words(`
	flushdb flushall shutdown swapdb migrate restore
	bgsave bgrewriteaof save failover replicaof slaveof psync sync
`)

// administering are the commands whose subcommand says what they do, and
// which administer the server when it does not say something else. A
// subcommand nobody listed is a change to the server.
var administering = words(`config client acl cluster script function debug module latency slowlog`)

// asking are the commands whose subcommand says what they do, and which read
// what the server holds when it does not say something else.
var asking = words(`object memory command xinfo xgroup json.debug`)

// reading are the subcommands that only read.
var reading = subcommands(`
	config get, client list, client info, client getname, client id,
	acl list, acl getuser, acl cat, acl whoami, acl users,
	cluster info, cluster nodes, cluster slots, cluster shards, cluster myid,
	cluster countkeysinslot, cluster getkeysinslot, cluster links,
	command count, command docs, command info, command list, command getkeys,
	object encoding, object freq, object idletime, object refcount, object help,
	memory usage, memory stats, memory doctor, memory help,
	latency history, latency latest, latency doctor,
	slowlog get, slowlog len, slowlog help,
	script exists, function list, function dump, function stats,
	xinfo stream, xinfo groups, xinfo consumers, json.debug memory
`)

// commandAccess says what a command does.
func commandAccess(args []string) source.Access {
	if len(args) == 0 {
		return source.AccessDDL
	}
	name := strings.ToLower(args[0])
	pair := name
	if len(args) > 1 {
		pair = name + " " + strings.ToLower(args[1])
	}
	if administering[name] || asking[name] {
		switch {
		case reading[pair]:
			return source.AccessRead
		case administering[name]:
			// CONFIG SET, CLIENT KILL, SCRIPT FLUSH: a subcommand of these
			// that is not one of the reading ones administers the server,
			// and one nobody listed is taken to as well.
			return source.AccessDDL
		}
		return source.AccessWrite
	}
	if serverChanging[name] {
		return source.AccessDDL
	}
	if readOnly[name] {
		return source.AccessRead
	}
	return source.AccessWrite
}

// words reads a list of names into a set.
func words(list string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(list) {
		out[strings.ToLower(name)] = true
	}
	return out
}

// subcommands reads a list of two-word names — a command and its subcommand
// — into a set. They are separated by commas so that both words read as one
// name.
func subcommands(list string) map[string]bool {
	out := map[string]bool{}
	for _, entry := range strings.Split(list, ",") {
		if name := strings.Join(strings.Fields(entry), " "); name != "" {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}
